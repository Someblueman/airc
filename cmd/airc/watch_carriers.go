package main

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// carrierPoll is how often rooms followed over a gateway without check are
// read again once caught up, backing off to carrierPollIdle while none has
// news.
// Reads, unlike gateway watches, have no per-author limit on how many rooms
// one client follows.
var carrierPoll, carrierPollIdle = time.Second, 4 * time.Second

// carrierWait is how long a gateway check may wait for news in quiet rooms;
// the gateway and the call's timeout may cut it shorter.
var carrierWait = 20 * time.Second

// maxCheckRooms is how many rooms one gateway check reads.
const maxCheckRooms = 16

// roomPage is one read of one room; the first of each connection has no room
// or messages and names the carrier.
type roomPage struct {
	room string
	page *fallback.Page
}

// followRooms reads each room over fb's gateway carriers from its start (a
// message ID, or "" for the latest) on one connection, again every
// carrierPoll once caught up, and reconnects with backoff. With inbox, it
// also follows the user's own inbox from there; its pages are delivered as
// room "@" and the nickname, and stop if the gateway serves no inbox. It
// delivers the pages that have messages or a gap until ctx ends; wait waits
// for it to stop.
func followRooms(ctx context.Context, fb *fallback.Client, starts map[string]string, inbox *string) (pages <-chan roomPage, wait func()) {
	out := make(chan roomPage)
	done := make(chan struct{})
	after := maps.Clone(starts)
	rooms := slices.Sorted(maps.Keys(starts))
	f := &follow{rooms: rooms, after: after, inboxName: "@" + fb.Nick}
	if inbox != nil {
		start := *inbox
		f.inbox = &start
	}
	deliver := func(p roomPage) bool {
		select {
		case out <- p:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(done)
		retry := newBackoff(250*time.Millisecond, 5*time.Second, true)
		for ctx.Err() == nil {
			if reader, err := fb.ReadRooms(ctx); err == nil {
				retry.reset()
				f.poll(ctx, reader, deliver)
				_ = reader.Close()
			}
			if !retry.wait(ctx) {
				return
			}
		}
	}()
	return out, func() { <-done }
}

// follow is what followRooms follows and where each part continues.
type follow struct {
	rooms     []string
	after     map[string]string
	inbox     *string // the inbox's position; nil when not followed
	inboxName string
}

// poll reads the followed rooms and inbox on reader until it fails or ctx
// ends: one check covers them all and waits on the gateway for news; a
// gateway without check is read room by room every carrierPoll, backing
// off while quiet.
func (f *follow) poll(ctx context.Context, reader *fallback.RoomReader, deliver func(roomPage) bool) {
	if !deliver(roomPage{page: &fallback.Page{Endpoint: reader.Endpoint()}}) {
		return
	}
	pause := carrierPoll
	for {
		// One check covers at most maxCheckRooms rooms and inbox; only a
		// single one can wait, so more are read in turn and paced here.
		targets := len(f.rooms)
		if f.inbox != nil {
			targets++
		}
		wait := carrierWait
		if targets > maxCheckRooms {
			wait = 0
		}
		news, more, start := false, false, time.Now()
		rooms := f.rooms
		for first := true; first || len(rooms) > 0; first = false {
			set := fallback.CheckSet{Limit: 100, Wait: wait}
			take := maxCheckRooms
			if first && f.inbox != nil {
				set.Inbox = f.inbox
				take--
			}
			take = min(take, len(rooms))
			for _, room := range rooms[:take] {
				set.Rooms = append(set.Rooms, fallback.RoomAfter{Room: room, After: f.after[room]})
			}
			rooms = rooms[take:]
			if len(set.Rooms) == 0 && set.Inbox == nil {
				break
			}
			checked, err := reader.CheckAll(ctx, set)
			if err != nil {
				return
			}
			for i, page := range checked.Rooms {
				room := set.Rooms[i].Room
				if n := len(page.Messages); n > 0 {
					f.after[room], news = page.Messages[n-1].ID, true
				} else if page.Gap {
					f.after[room] = "" // nothing retained after the expired start
				}
				if (len(page.Messages) > 0 || page.Gap) && !deliver(roomPage{room, page}) {
					return
				}
				more = more || page.Status == "more"
			}
			if set.Inbox != nil {
				page := checked.Inbox
				if page == nil {
					f.inbox = nil // the gateway serves no inbox
					continue
				}
				// The carrier's own place in the inbox. A mentions-only
				// inbox leaves its direct messages for native IRC, whose
				// session catches them up from the position the watcher
				// keeps, not from this one.
				switch n := len(page.Messages); {
				case n > 0:
					*f.inbox, news = page.Messages[n-1].ID, true
				case page.Status == "ok" && page.Cursor != "":
					*f.inbox = page.Cursor
				}
				if (len(page.Messages) > 0 || page.Gap) && !deliver(roomPage{f.inboxName, page}) {
					return
				}
				more = more || page.Status == "more"
			}
		}
		if more {
			continue
		}
		// With a waiting gateway, rounds start at most every carrierPoll:
		// a quiet round already waited, and busy rooms gather news for
		// the next round instead of costing a request per message.
		if wait > 0 && reader.Waits() {
			pause = carrierPoll - time.Since(start)
		} else if news {
			pause = carrierPoll
		}
		timer := time.NewTimer(max(pause, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if !news && !(wait > 0 && reader.Waits()) {
			pause = min(pause*2, carrierPollIdle)
		}
	}
}

// bridge follows the watched rooms, and the user's own inbox, over the
// server's other carriers while native IRC is unreachable, from the last
// message shown in each, so the watcher misses nothing and shows nothing
// twice. Other direct messages, and a guest's own, wait for native IRC,
// whose session then catches them up; its catch-up skips what was shown.
func (w *watcher) bridge(ctx context.Context, back <-chan struct{}) error {
	starts := map[string]string{}
	var inbox *string
	dms := false
	for _, target := range w.targets {
		switch {
		case protocol.IsChannel(target.history):
			starts[target.history] = w.last[targetKey(target.history)]
		case strings.EqualFold(target.history, w.opt.nick) && w.opt.nick != "":
			// The user's own inbox continues over the gateway; a guest's
			// direct messages there wait for native IRC.
			start := w.last[targetKey(target.history)]
			inbox = &start
			dms = dms || !hasIdentity(w.opt)
		default:
			dms = true
		}
	}
	if len(starts) == 0 && inbox == nil {
		return nil
	}
	fb, _, err := racingClient(w.opt, preferLast)
	if err != nil || fb == nil {
		return err
	}
	ctx, cancel := untilBack(ctx, back)
	pages, wait := followRooms(ctx, fb, starts, inbox)
	defer wait()
	defer cancel()
	announced := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case d := <-pages:
			if carrier := d.page.Endpoint.String(); carrier != announced {
				announced = carrier
				if err := w.continuing(carrier, dms); err != nil {
					return err
				}
			}
			if d.page.Gap {
				if err := w.show("some messages in "+d.room+" from while native IRC was unreachable are no longer available", false); err != nil {
					return err
				}
			}
			for _, m := range d.page.Messages {
				if err := w.emit(historyEvent(m).MessageEvent()); err != nil {
					return err
				}
			}
		}
	}
}

// untilBack is ctx, also cancelled once back closes.
func untilBack(ctx context.Context, back <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-back:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// continuing reports that the watch goes on over another carrier.
func (w *watcher) continuing(carrier string, dms bool) error {
	if w.json {
		return w.encoder.Encode(&irc.ConnectionEvent{Type: "connection", Connected: true, Carrier: carrier})
	}
	return w.write(w.view.info(continuingText(carrier, dms), true))
}

func continuingText(carrier string, dms bool) string {
	text := "following rooms over " + carrier + " while native IRC is unreachable"
	if dms {
		text += "; direct messages follow when it returns"
	}
	return text
}

// hasIdentity reports a user with an identity (a registered account), whose
// direct messages a gateway can prove.
func hasIdentity(opt options) bool {
	cfg, err := dialConfig(opt)
	return err == nil && cfg.IdentityToken != ""
}
