package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// overCarriers ends a native check attempt whose connection race another
// carrier won: the check continues over the server's other carriers.
type overCarriers struct{ fb *fallback.Client }

func (*overCarriers) Error() string { return "native IRC is unreachable" }

// carrierCheckable reports a check whose reads can continue over gateway
// carriers: followed rooms only, since gateways serve no inbox, mentions or
// reply selectors.
func carrierCheckable(settings *checkOptions, followed []checkTarget) bool {
	return settings.replyTo == "" && !settings.mentions && !settings.fromNow && len(followed) > 0
}

// checkOverCarriers runs a check's room reads over the server's other
// carriers while native IRC is unreachable. Cursors are message IDs, as
// native IRC keeps them, so the next native check continues from here. The
// inbox and followed threads wait for native IRC; a warning says so. A wait
// reads the rooms again every carrierPoll, over one connection that is
// replaced with backoff when it fails, until the deadline.
func checkOverCarriers(ctx context.Context, opt options, settings *checkOptions, fb *fallback.Client, stdout, stderr io.Writer) error {
	followed, err := settings.targets(opt.nick)
	if err != nil {
		return err
	}
	c := &checker{nick: opt.nick, settings: settings, targets: followed}
	waiting := "Native IRC is unreachable: direct messages, mentions and followed threads wait for it; rooms were read over another carrier"
	expired := func(err error, phase string) error {
		if settings.wait == 0 || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return failure(err, phase)
		}
		if opt.json {
			return json.NewEncoder(stdout).Encode(checkStatus{Type: "status", Code: "wait_expired", Warnings: []string{waiting}})
		}
		return nil
	}
	retry := newBackoff(250*time.Millisecond, 5*time.Second, true)
	var reader *fallback.RoomReader
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()
	for {
		if reader == nil {
			if reader, err = fb.ReadRooms(ctx); err != nil {
				if settings.wait > 0 && ctx.Err() == nil && retry.wait(ctx) {
					continue
				}
				return expired(err, "login")
			}
		}
		done, err := c.checkRoomsOnce(ctx, opt, reader, waiting, stdout, stderr)
		if done {
			return err
		}
		if fallback.Definite(err) {
			return failure(err, "history")
		}
		if err != nil {
			_ = reader.Close()
			reader = nil
			if settings.wait > 0 && ctx.Err() == nil && retry.wait(ctx) {
				continue
			}
			return expired(err, "history")
		}
		retry.reset()
		timer := time.NewTimer(carrierPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return expired(ctx.Err(), "wait")
		case <-timer.C:
		}
	}
}

// checkRoomsOnce reads the followed rooms after their cursors under the
// cursor lock and outputs them, reporting done, or commits what it filtered
// and reports a wait is due.
func (c *checker) checkRoomsOnce(ctx context.Context, opt options, reader *fallback.RoomReader, waiting string, stdout, stderr io.Writer) (done bool, err error) {
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
		batch.warnings = append(batch.warnings, waiting)
		return true, c.output(batch, nil, store, opt.json, stdout, stderr)
	}
	if err := c.output(batch, nil, store, false, io.Discard, stderr); err != nil {
		return true, err
	}
	return false, nil
}

// fetchOverCarriers reads each followed room after its cursor, as fetchNew
// does natively, one page per room.
func (c *checker) fetchOverCarriers(ctx context.Context, reader *fallback.RoomReader, cursors map[string]string) (checkBatch, error) {
	var batch checkBatch
	for _, target := range c.targets {
		after := cursors[target.key]
		limit := min(c.settings.limit, c.settings.maxMessages+1)
		if after == "" {
			limit = c.settings.initial
		}
		page, err := reader.Read(ctx, target.name, after, limit)
		if err != nil {
			return checkBatch{}, err
		}
		if page.Gap {
			batch.gaps = append(batch.gaps, target.name)
		}
		read := targetRead{target: target, more: page.Status == "more"}
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

// historyEvent is a gateway message in the shape of a native history entry.
func historyEvent(m *irc.MessageMetadata) *irc.HistoryEvent {
	return &irc.HistoryEvent{Type: "history", ChatMetadata: m.ChatMetadata, ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, Seq: m.Seq, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
}
