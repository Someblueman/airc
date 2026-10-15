package main

import (
	"context"
	"maps"
	"slices"
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
// carrierPoll once caught up, and reconnects with backoff. It delivers the
// pages that have messages or a gap until ctx ends; wait waits for it to stop.
func followRooms(ctx context.Context, fb *fallback.Client, starts map[string]string) (pages <-chan roomPage, wait func()) {
	out := make(chan roomPage)
	done := make(chan struct{})
	after := maps.Clone(starts)
	rooms := slices.Sorted(maps.Keys(starts))
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
				pollRooms(ctx, reader, rooms, after, deliver)
				_ = reader.Close()
			}
			if !retry.wait(ctx) {
				return
			}
		}
	}()
	return out, func() { <-done }
}

// pollRooms reads rooms on reader until it fails or ctx ends: one check
// covers every room and waits on the gateway for news; a gateway without
// check is read room by room every carrierPoll, backing off while quiet.
func pollRooms(ctx context.Context, reader *fallback.RoomReader, rooms []string, after map[string]string, deliver func(roomPage) bool) {
	if !deliver(roomPage{page: &fallback.Page{Endpoint: reader.Endpoint()}}) {
		return
	}
	// One check covers at most maxCheckRooms rooms; only a single one can
	// wait, so more rooms are read in turn and paced here.
	wait := carrierWait
	if len(rooms) > maxCheckRooms {
		wait = 0
	}
	pause := carrierPoll
	for {
		news, more, start := false, false, time.Now()
		for chunk := range slices.Chunk(rooms, maxCheckRooms) {
			reads := make([]fallback.RoomAfter, len(chunk))
			for i, room := range chunk {
				reads[i] = fallback.RoomAfter{Room: room, After: after[room]}
			}
			pages, err := reader.Check(ctx, reads, 100, wait)
			if err != nil {
				return
			}
			for i, page := range pages {
				room := chunk[i]
				if n := len(page.Messages); n > 0 {
					after[room], news = page.Messages[n-1].ID, true
				} else if page.Gap {
					after[room] = "" // nothing retained after the expired start
				}
				if (len(page.Messages) > 0 || page.Gap) && !deliver(roomPage{room, page}) {
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

// bridge follows the watched rooms over the server's other carriers while
// native IRC is unreachable, from the last message shown in each, so the
// watcher misses nothing and shows nothing twice. Direct messages wait for
// native IRC, whose session then catches them up.
func (w *watcher) bridge(ctx context.Context, back <-chan struct{}) error {
	starts := map[string]string{}
	dms := false
	for _, target := range w.targets {
		if protocol.IsChannel(target.history) {
			starts[target.history] = w.last[targetKey(target.history)]
		} else {
			dms = true
		}
	}
	if len(starts) == 0 {
		return nil
	}
	fb, _, err := racingClient(w.opt, preferLast)
	if err != nil || fb == nil {
		return err
	}
	ctx, cancel := untilBack(ctx, back)
	pages, wait := followRooms(ctx, fb, starts)
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
