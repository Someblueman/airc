package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

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
// inbox and followed threads wait for native IRC; a warning says so.
func checkOverCarriers(ctx context.Context, opt options, settings *checkOptions, fb *fallback.Client, stdout, stderr io.Writer) error {
	followed, err := settings.targets(opt.nick)
	if err != nil {
		return err
	}
	store, err := openCursors(opt, opt.nick)
	if err != nil {
		return err
	}
	defer func() {
		if store != nil {
			store.close()
		}
	}()
	if store.warning != "" {
		fmt.Fprintln(stderr, "airc: warning:", store.warning)
	}
	c := &checker{nick: opt.nick, settings: settings, targets: followed}
	waiting := "Native IRC is unreachable: direct messages, mentions and followed threads wait for it; rooms were read over another carrier"
	for {
		batch, err := c.fetchOverCarriers(ctx, fb, store.Cursors)
		if err != nil {
			return failure(err, "history")
		}
		batch.warnings = append(batch.warnings, waiting)
		if batch.hasVisible || batch.more || len(batch.gaps) > 0 || settings.wait == 0 {
			return c.output(batch, nil, store, opt.json, stdout, stderr)
		}
		quiet := batch
		quiet.warnings = nil
		if err := c.output(quiet, nil, store, false, io.Discard, stderr); err != nil {
			return err
		}
		cursors := store.Cursors
		store.close()
		store = nil
		if err := c.waitOverCarriers(ctx, fb, cursors); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if opt.json {
					return json.NewEncoder(stdout).Encode(checkStatus{Type: "status", Code: "wait_expired", Warnings: []string{waiting}})
				}
				return nil
			}
			return failure(err, "wait")
		}
		if store, err = openCursors(opt, opt.nick); err != nil {
			return err
		}
	}
}

// fetchOverCarriers reads each followed room after its cursor, as fetchNew
// does natively, one page per room.
func (c *checker) fetchOverCarriers(ctx context.Context, fb *fallback.Client, cursors map[string]string) (checkBatch, error) {
	var batch checkBatch
	for _, target := range c.targets {
		after := cursors[target.key]
		limit := min(1000, c.settings.maxMessages+1)
		if after == "" {
			limit = c.settings.initial
		}
		page, err := fb.RoomHistory(ctx, target.name, after, limit)
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

// waitOverCarriers watches every followed room from its cursor until one has
// a message this check would show.
func (c *checker) waitOverCarriers(ctx context.Context, fb *fallback.Client, cursors map[string]string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rang := make(chan error, len(c.targets))
	var wg sync.WaitGroup
	for _, target := range c.targets {
		wg.Go(func() {
			rang <- c.watchOverCarriers(ctx, fb, target.name, cursors[target.key])
		})
	}
	defer wg.Wait()
	select {
	case err := <-rang:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *checker) watchOverCarriers(ctx context.Context, fb *fallback.Client, room, after string) error {
	w, err := fb.WatchRoom(ctx, room, after, 100)
	if err != nil {
		return err
	}
	defer w.Close()
	for {
		page, err := w.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, m := range page.Messages {
			if c.settings.includeOwn || !strings.EqualFold(m.From, c.nick) {
				return nil
			}
		}
	}
}

// historyEvent is a gateway message in the shape of a native history entry.
func historyEvent(m *irc.MessageMetadata) *irc.HistoryEvent {
	return &irc.HistoryEvent{Type: "history", ChatMetadata: m.ChatMetadata, ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, Seq: m.Seq, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
}
