package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// overCarriers ends a native check attempt whose connection race another
// carrier won: the check continues over the server's other carriers.
type overCarriers struct {
	fb   *fallback.Client
	conn *fallback.Conn // the gateway connection that won
}

func (*overCarriers) Error() string { return "native IRC is unreachable" }

// carrierCheckable reports a check whose reads can continue over gateway
// carriers: followed rooms and threads, and the inbox. Gateways serve no
// reply selectors.
func carrierCheckable(settings *checkOptions) bool {
	return settings.replyTo == "" && !settings.fromNow
}

// checkOverCarriers runs a check's room reads over the server's other
// carriers while native IRC is unreachable, starting on conn. Cursors are
// message IDs, as native IRC keeps them, so the next native check continues
// from here. Followed threads and the inbox are read too: a thread with
// direct messages, and the direct messages of an inbox the gateway serves
// without them (a guest's), wait for native IRC, and a warning says so. A
// wait reads them again every carrierPoll, over one
// connection that is replaced with backoff when it fails, while native IRC
// is redialled; once it registers, the wait continues there (back).
func checkOverCarriers(ctx context.Context, opt options, settings *checkOptions, fb *fallback.Client, conn *fallback.Conn, stdout, stderr io.Writer) (back *irc.Client, err error) {
	reader, err := conn.ReadRooms()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()
	followed, err := settings.targets(opt.nick)
	if err != nil {
		return nil, err
	}
	c := &checker{nick: opt.nick, settings: settings}
	inbox := checkTarget{name: "@" + opt.nick, key: c.inboxKey(), watch: "@" + opt.nick}
	if settings.mentions {
		c.targets = []checkTarget{inbox}
	} else {
		c.targets = append(slices.Clip(followed), inbox)
		store, err := openCursors(opt, opt.nick)
		if err != nil {
			return nil, err
		}
		for _, id := range store.Follows {
			c.targets = append(c.targets, checkTarget{name: "thread:" + id, key: "thread:" + id, watch: "thread:" + id})
		}
		store.close()
	}
	waiting := c.carrierNote
	expired := func(err error, phase string) error {
		if settings.wait == 0 || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failure(err, phase)
		}
		if opt.json {
			return json.NewEncoder(stdout).Encode(checkStatus{Type: "status", Code: "wait_expired", Warnings: []string{waiting()}})
		}
		return nil
	}
	// native holds the redialled connection once it registers, and
	// nativeUp ends then, cutting short a wait on the gateway.
	native := make(chan *irc.Client, 1)
	nativeUp := ctx
	if settings.wait > 0 {
		redial, stop := context.WithCancel(ctx)
		up, back := context.WithCancel(ctx)
		nativeUp = up
		forwarded := make(chan struct{})
		go func() {
			defer close(forwarded)
			select {
			case client := <-redialNative(redial, opt):
				native <- client
				back()
			case <-redial.Done():
			}
		}()
		defer func() {
			// Stop redialling, then close a connection that registered as
			// the check ended rather than continuing on it.
			stop()
			<-forwarded
			back()
			select {
			case client := <-native:
				client.Close()
			default:
			}
		}()
	}
	retry := newBackoff(250*time.Millisecond, 5*time.Second, true)
	for round := time.Now(); ; round = time.Now() {
		if reader == nil {
			if reader, err = fb.ReadRooms(ctx); err != nil {
				if settings.wait > 0 && ctx.Err() == nil && retry.wait(ctx) {
					continue
				}
				return nil, expired(err, "login")
			}
		}
		done, err := c.checkRoomsOnce(ctx, opt, reader, waiting, stdout, stderr)
		if done {
			return nil, err
		}
		if fallback.Definite(err) {
			return nil, failure(err, "history")
		}
		if err != nil {
			_ = reader.Close()
			reader = nil
			if settings.wait > 0 && ctx.Err() == nil && retry.wait(ctx) {
				continue
			}
			return nil, expired(err, "history")
		}
		retry.reset()
		// A gateway with check waits for news itself, outside the cursor
		// lock; the next round reads it under the lock. Either way rounds
		// start at most every carrierPoll.
		c.awaitNews(nativeUp, opt, reader)
		select {
		case client := <-native:
			return client, nil
		default:
		}
		pause := carrierPoll - time.Since(round)
		timer := time.NewTimer(max(pause, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, expired(ctx.Err(), "wait")
		case client := <-native:
			timer.Stop()
			return client, nil
		case <-timer.C:
		}
	}
}

// awaitNews waits on the gateway, up to carrierWait, for news after the
// followed rooms', threads' and inbox's cursors, when every one has a
// cursor and one check covers them all. What it reads is only a wake-up; a
// failure surfaces in the next round.
func (c *checker) awaitNews(ctx context.Context, opt options, reader *fallback.RoomReader) {
	if !reader.Waits() || len(c.targets) > maxCheckRooms {
		return
	}
	store, err := openCursors(opt, opt.nick)
	if err != nil {
		return
	}
	cursors := maps.Clone(store.Cursors)
	store.close()
	set := fallback.CheckSet{Limit: 1, Wait: carrierWait}
	inbox := c.inboxTarget()
	for _, target := range c.targets {
		after := cursors[target.key]
		switch {
		case target.key == inbox.key:
			start := c.inboxAfter(cursors)
			set.Inbox = &start
			continue
		case after == "":
			return
		case strings.HasPrefix(target.key, "thread:"):
			set.Threads = append(set.Threads, fallback.ThreadAfter{ID: strings.TrimPrefix(target.key, "thread:"), After: after})
		default:
			set.Rooms = append(set.Rooms, fallback.RoomAfter{Room: target.name, After: after})
		}
	}
	if reader.Follows() {
		// One stream carries the wake-up: keepalives while quiet, the
		// first news ends it. A lease ending without news is a wake-up
		// too; the round that follows reads nothing and waits again.
		woke := errors.New("news")
		err := reader.Follow(ctx, set, followIdle, func(checked fallback.Checked) error {
			if checkedHasNews(checked) {
				return woke
			}
			return nil
		})
		if !errors.Is(err, fallback.ErrNoFollow) {
			return
		}
	}
	_, _ = reader.CheckAll(ctx, set)
}

// checkedHasNews reports a check answer with a message, a gap or more
// waiting on any page.
func checkedHasNews(checked fallback.Checked) bool {
	pages := append(slices.Clone(checked.Rooms), checked.Threads...)
	if checked.Inbox != nil {
		pages = append(pages, checked.Inbox)
	}
	for _, page := range pages {
		if len(page.Messages) > 0 || page.Gap || page.Status == "more" {
			return true
		}
	}
	return false
}

// The first delay before a check waiting over another carrier redials
// native IRC; a variable so tests can shorten it.
var nativeRedialBackoff = time.Second

// redialNative dials native IRC with backoff until a connection registers,
// which it sends, or ctx ends.
func redialNative(ctx context.Context, opt options) <-chan *irc.Client {
	out := make(chan *irc.Client)
	go func() {
		retry := newBackoff(nativeRedialBackoff, 10*time.Second, true)
		for retry.wait(ctx) {
			client, err := dialSession(ctx, opt, true)
			if err != nil {
				continue
			}
			select {
			case out <- client:
			case <-ctx.Done():
				client.Close()
			}
			return
		}
	}()
	return out
}

// checkRoomsOnce reads the followed rooms after their cursors under the
// cursor lock and outputs them, reporting done, or commits what it filtered
// and reports a wait is due.
func (c *checker) checkRoomsOnce(ctx context.Context, opt options, reader *fallback.RoomReader, waiting func() string, stdout, stderr io.Writer) (done bool, err error) {
	store, err := openCursors(opt, opt.nick)
	if err != nil {
		return true, err
	}
	defer store.close()
	if store.warning != "" {
		fmt.Fprintln(stderr, "airc: warning:", store.warning)
	}
	batch, err := c.fetchOverCarriers(ctx, reader, store.Cursors)
	if err != nil {
		return false, err
	}
	if batch.hasVisible || batch.more || len(batch.gaps) > 0 || c.settings.wait == 0 {
		batch.warnings = append(batch.warnings, waiting())
		return true, c.output(batch, nil, store, opt.json, stdout, stderr)
	}
	if err := c.output(batch, nil, store, false, io.Discard, stderr); err != nil {
		return true, err
	}
	return false, nil
}

// fetchOverCarriers reads each followed room and thread, and the inbox,
// after its cursor, as fetchNew does natively, one page per target. They
// are read in checks of up to maxCheckRooms, the first with the inbox; a
// room without a cursor is read alone, for its initial page, while a thread
// or inbox without one starts at the oldest retained message.
func (c *checker) fetchOverCarriers(ctx context.Context, reader *fallback.RoomReader, cursors map[string]string) (checkBatch, error) {
	var batch checkBatch
	c.notes = nil
	pages := map[string]*fallback.Page{}
	var rooms []fallback.RoomAfter
	var threads []fallback.ThreadAfter
	inbox := c.inboxTarget()
	for _, target := range c.targets {
		switch after := cursors[target.key]; {
		case target.key == inbox.key:
		case strings.HasPrefix(target.key, "thread:"):
			if after == "" {
				after = "retained"
			}
			threads = append(threads, fallback.ThreadAfter{ID: strings.TrimPrefix(target.key, "thread:"), After: after})
		case after != "":
			rooms = append(rooms, fallback.RoomAfter{Room: target.name, After: after})
		}
	}
	limit := min(c.settings.limit, c.settings.maxMessages+1)
	first := true
	for len(rooms) > 0 || len(threads) > 0 || first && inbox.key != "" {
		set := fallback.CheckSet{Limit: limit}
		room := maxCheckRooms
		if first && inbox.key != "" {
			start := c.inboxAfter(cursors)
			set.Inbox, room = &start, maxCheckRooms-1
		}
		take := min(len(rooms), room)
		set.Rooms, rooms = rooms[:take], rooms[take:]
		take = min(len(threads), room-take)
		set.Threads, threads = threads[:take], threads[take:]
		checked, err := reader.CheckAll(ctx, set)
		if err != nil {
			return checkBatch{}, err
		}
		for i, page := range checked.Rooms {
			pages[set.Rooms[i].Room] = page
		}
		if len(set.Threads) > 0 && checked.Threads == nil {
			c.notes = append(c.notes, "followed threads wait for native IRC")
		}
		for i, page := range checked.Threads {
			id := set.Threads[i].ID
			switch page.Status {
			case "withheld":
				c.notes = append(c.notes, "followed thread "+id+" has direct messages, which wait for native IRC")
			case "expired":
				c.notes = append(c.notes, "Followed thread:"+id+" expired; use airc unfollow to remove it")
			default:
				pages["thread:"+id] = page
			}
		}
		if first && inbox.key != "" {
			switch {
			case checked.Inbox == nil:
				c.dropInbox()
			case checked.Inbox.MentionsOnly:
				c.mentionsOnly = true
				pages[inbox.name] = checked.Inbox
			default:
				c.mentionsOnly = false
				pages[inbox.name] = checked.Inbox
			}
		}
		first = false
	}
	for _, target := range c.targets {
		page := pages[target.name]
		if page == nil {
			if !protocol.IsChannel(target.name) || cursors[target.key] != "" {
				continue // a thread or inbox this round did not serve
			}
			var err error
			if page, err = reader.Read(ctx, target.name, "", c.settings.initial); err != nil {
				return checkBatch{}, err
			}
		}
		if target.key == inbox.key && c.mentionsOnly {
			// Keep native IRC's inbox cursor for the direct messages this
			// carrier did not serve; see carrierInboxKey.
			target.key = carrierInboxKey(inbox.key)
		}
		if page.Gap {
			batch.gaps = append(batch.gaps, target.name)
		}
		// An expired cursor with nothing retained after it is forgotten, so
		// the next check starts the room afresh instead of at the same gap.
		read := targetRead{target: target, more: page.Status == "more", reset: page.Gap && len(page.Messages) == 0}
		read.page.status = page.Status // the gateway's cursor is not a native one
		for _, m := range page.Messages {
			h := historyEvent(m)
			read.page.messages = append(read.page.messages, h)
			batch.hasVisible = batch.hasVisible || c.visible(h)
		}
		batch.more = batch.more || read.more
		batch.reads = append(batch.reads, read)
	}
	return batch, nil
}

// inboxAfter is where the inbox continues over another carrier: its own
// place in a mentions-only inbox, else the native inbox cursor, else the
// oldest retained message.
func (c *checker) inboxAfter(cursors map[string]string) string {
	inbox := c.inboxKey()
	if after := cursors[carrierInboxKey(inbox)]; after != "" {
		return after
	}
	if after := cursors[inbox]; after != "" {
		return after
	}
	return "retained"
}

// inboxKey is the cursor key of this nickname's inbox.
func (c *checker) inboxKey() string { return "@" + strings.ToLower(c.nick) }

// inboxTarget is the check's inbox target, or a zero one without it.
func (c *checker) inboxTarget() checkTarget {
	for _, target := range c.targets {
		if target.key == c.inboxKey() {
			return target
		}
	}
	return checkTarget{}
}

// dropInbox stops reading an inbox the gateway does not serve; it waits for
// native IRC, and the carrier note says so.
func (c *checker) dropInbox() {
	key := c.inboxKey()
	c.targets = slices.DeleteFunc(c.targets, func(t checkTarget) bool { return t.key == key })
}

// carrierNote is the warning a check over another carrier carries.
func (c *checker) carrierNote() string {
	note := "Native IRC is unreachable: rooms, followed threads and the inbox were read over another carrier"
	switch {
	case c.inboxTarget().key == "":
		note = "Native IRC is unreachable: direct messages and mentions wait for it; rooms and followed threads were read over another carrier"
	case c.mentionsOnly:
		note = "Native IRC is unreachable: direct messages wait for it; rooms, followed threads and mentions were read over another carrier"
	}
	for _, extra := range c.notes {
		note += "; " + extra
	}
	return note
}

// historyEvent is a gateway message in the shape of a native history entry.
func historyEvent(m *irc.MessageMetadata) *irc.HistoryEvent {
	return &irc.HistoryEvent{Type: "history", ChatMetadata: m.ChatMetadata, ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, Seq: m.Seq, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
}
