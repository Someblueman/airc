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
// carriers: followed rooms, and the inbox for a registered user, whose
// account is what proves a direct message was theirs. Gateways serve no
// reply selectors.
func carrierCheckable(settings *checkOptions, followed []checkTarget, registered bool) bool {
	return settings.replyTo == "" && !settings.fromNow && (registered || !settings.mentions && len(followed) > 0)
}

// hasIdentity reports a user with an identity (a registered account).
func hasIdentity(opt options) bool {
	cfg, err := dialConfig(opt)
	return err == nil && cfg.IdentityToken != ""
}

// checkOverCarriers runs a check's room reads over the server's other
// carriers while native IRC is unreachable, starting on conn. Cursors are
// message IDs, as native IRC keeps them, so the next native check continues
// from here. A registered user's inbox is read too, under the inbox cursor
// native IRC keeps; a guest's inbox, and followed threads, wait for native
// IRC, and a warning says so. A wait reads the rooms again every carrierPoll, over one
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
	c := &checker{nick: opt.nick, settings: settings, targets: followed}
	if reader.ServesInbox() {
		inbox := checkTarget{name: "@" + opt.nick, key: "@" + strings.ToLower(opt.nick), watch: "@" + opt.nick}
		if settings.mentions {
			c.targets = []checkTarget{inbox}
		} else {
			c.targets = append(slices.Clip(followed), inbox)
		}
	}
	if len(c.targets) == 0 {
		return nil, errors.New("native IRC is unreachable, and only it serves this check")
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
	var native <-chan *irc.Client
	if settings.wait > 0 {
		redial, stop := context.WithCancel(ctx)
		defer stop()
		native = redialNative(redial, opt)
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
		c.awaitNews(ctx, opt, reader)
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
// followed rooms' cursors, when every room has a cursor, one check covers
// them all and the gateway has check. What it reads is only a wake-up; a
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
	inbox := c.inboxTarget()
	var reads []fallback.RoomAfter
	for _, target := range c.targets {
		if cursors[target.key] == "" {
			return
		}
		if target.key != inbox.key {
			reads = append(reads, fallback.RoomAfter{Room: target.name, After: cursors[target.key]})
		}
	}
	if inbox.key != "" {
		_, _, _ = reader.CheckInbox(ctx, reads, cursors[inbox.key], 1, carrierWait)
		return
	}
	_, _ = reader.Check(ctx, reads, 1, carrierWait)
}

// redialNative dials native IRC with backoff until a connection registers,
// which it sends, or ctx ends.
func redialNative(ctx context.Context, opt options) <-chan *irc.Client {
	out := make(chan *irc.Client)
	go func() {
		retry := newBackoff(time.Second, 10*time.Second, true)
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

// fetchOverCarriers reads each followed room, and a registered user's
// inbox, after its cursor, as fetchNew does natively, one page per target.
func (c *checker) fetchOverCarriers(ctx context.Context, reader *fallback.RoomReader, cursors map[string]string) (checkBatch, error) {
	var batch checkBatch
	// Rooms with cursors are read in checks of up to maxCheckRooms targets,
	// the first with the inbox; a room without one is read alone, for its
	// initial page. An inbox starts at the oldest retained message.
	pages := map[string]*fallback.Page{}
	var reads []fallback.RoomAfter
	inbox := c.inboxTarget()
	for _, target := range c.targets {
		if after := cursors[target.key]; after != "" && target.key != inbox.key {
			reads = append(reads, fallback.RoomAfter{Room: target.name, After: after})
		}
	}
	limit := min(c.settings.limit, c.settings.maxMessages+1)
	if inbox.key != "" {
		first := reads[:min(len(reads), maxCheckRooms-1)]
		reads = reads[len(first):]
		after := cursors[inbox.key]
		if after == "" {
			after = "retained"
		}
		read, page, err := reader.CheckInbox(ctx, first, after, limit, 0)
		if err != nil {
			return checkBatch{}, err
		}
		for i, p := range read {
			pages[first[i].Room] = p
		}
		if page == nil {
			c.dropInbox()
		} else {
			pages[inbox.name] = page
		}
	}
	for chunk := range slices.Chunk(reads, maxCheckRooms) {
		read, err := reader.Check(ctx, chunk, limit, 0)
		if err != nil {
			return checkBatch{}, err
		}
		for i, page := range read {
			pages[chunk[i].Room] = page
		}
	}
	for _, target := range c.targets {
		page := pages[target.name]
		if page == nil {
			var err error
			if page, err = reader.Read(ctx, target.name, "", c.settings.initial); err != nil {
				return checkBatch{}, err
			}
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

// inboxTarget is the check's inbox target, or a zero one without it.
func (c *checker) inboxTarget() checkTarget {
	for _, target := range c.targets {
		if strings.HasPrefix(target.key, "@") {
			return target
		}
	}
	return checkTarget{}
}

// dropInbox stops reading an inbox the gateway refused; it waits for native
// IRC, and the carrier note says so.
func (c *checker) dropInbox() {
	c.targets = slices.DeleteFunc(c.targets, func(t checkTarget) bool { return strings.HasPrefix(t.key, "@") })
}

// carrierNote is the warning a check over another carrier carries.
func (c *checker) carrierNote() string {
	if c.inboxTarget().key != "" {
		return "Native IRC is unreachable: followed threads wait for it; rooms and the inbox were read over another carrier"
	}
	return "Native IRC is unreachable: direct messages, mentions and followed threads wait for it; rooms were read over another carrier"
}

// historyEvent is a gateway message in the shape of a native history entry.
func historyEvent(m *irc.MessageMetadata) *irc.HistoryEvent {
	return &irc.HistoryEvent{Type: "history", ChatMetadata: m.ChatMetadata, ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, Seq: m.Seq, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
}
