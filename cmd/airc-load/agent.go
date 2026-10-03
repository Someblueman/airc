package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// A deterministic rotating mix: 40% posts, 20% mentions, 10% each other action.
var mix = []string{"post", "mention", "reply", "check", "post", "react", "mention", "search", "post", "post"}

type phase struct {
	start    time.Time
	duration time.Duration
	measured bool
}
type agentResult struct {
	samples              []sample
	connected, ready     bool
	live, mentions, gaps uint64
}
type agent struct {
	index, total                   int
	cfg                            config
	client                         *irc.Client
	alive                          bool
	nick, room, root, peer, cursor string
	live, mentions, gaps           uint64
}

func (a *agent) event(e irc.Event) {
	if m, ok := e.(*irc.MessageEvent); ok {
		a.live++
		if m.Mentions(a.nick) {
			a.mentions++
		}
		if m.From != a.nick && m.Reaction == "" && m.Target == a.room {
			a.peer = m.ID
		}
	}
}

func (a *agent) receive(ctx context.Context, accept func(irc.Event) bool) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-a.client.Events():
			if !ok {
				a.alive = false
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return io.EOF
			}
			a.event(e)
			if r, ok := e.(*irc.RawEvent); ok && r.Command != "422" && len(r.Command) == 3 && r.Command[0] >= '4' && r.Command[0] <= '5' {
				return &irc.RejectedError{Code: r.Command, Message: r.Trailing}
			}
			if accept(e) {
				return nil
			}
		}
	}
}

func outcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var rejected *irc.RejectedError
	if errors.As(err, &rejected) {
		return "rejected_" + rejected.Code
	}
	if errors.Is(err, io.EOF) {
		return "disconnected"
	}
	return "invalid_or_transport_error"
}

func (a *agent) operate(ctx context.Context, op, key string) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			resultErr = ctx.Err()
		}
	}()
	// Close on deadline so a late response cannot be mistaken for the next action.
	stop := context.AfterFunc(ctx, func() { _ = a.client.Close() })
	defer stop()
	if op == "check" {
		entries, err := irc.RequestCheck(ctx, a.client, irc.CheckRequest{MaxMessages: 50, Targets: []irc.CheckTarget{{Target: a.room, After: a.cursor, Limit: 50}}}, a.event)
		if err != nil {
			return err
		}
		pages := 0
		for _, e := range entries {
			if e.Kind == "page" {
				pages++
				a.cursor = e.Cursor
				if e.Gap {
					a.gaps++
				}
			}
		}
		if pages != 1 {
			return errors.New("missing check page marker")
		}
		return nil
	}
	if op == "search" {
		if err := a.client.Search(a.room, "load-probe-absent", "*", "*", 50); err != nil {
			return err
		}
		return a.receive(ctx, func(e irc.Event) bool { end, ok := e.(*irc.EndOfHistoryEvent); return ok && end.Target == a.room })
	}
	parent := a.root
	if a.peer != "" {
		parent = a.peer
	}
	body := "evidence "
	if op == "mention" {
		peer := (a.index + 1) % a.total
		body = fmt.Sprintf("@load%04d evidence ", peer)
	}
	body += strings.Repeat("x", a.cfg.BodyBytes-len(body))
	var err error
	switch op {
	case "post", "mention":
		err = a.client.SendWithID(a.room, body, key)
	case "reply":
		err = a.client.ReplyWithID(parent, body, key)
	case "react":
		err = a.client.React(parent, "agree")
	default:
		return errors.New("unknown operation")
	}
	if err != nil {
		return err
	}
	var receipt *irc.SendReceiptEvent
	err = a.receive(ctx, func(e irc.Event) bool {
		r, ok := e.(*irc.SendReceiptEvent)
		if !ok {
			return false
		}
		if op == "react" {
			if r.ReplyTo != parent || r.Reaction != "agree" {
				return false
			}
		} else if r.RequestID != key {
			return false
		}
		receipt = r
		return true
	})
	if err != nil {
		return err
	}
	if receipt.From != a.nick || receipt.Target != a.room || receipt.Receipt == nil || !receipt.Receipt.Accepted || receipt.Receipt.Persisted != a.cfg.Persist {
		return errors.New("receipt contract mismatch")
	}
	if op != "react" && receipt.Message != body {
		return errors.New("receipt body mismatch")
	}
	if op == "reply" && receipt.ReplyTo != parent {
		return errors.New("receipt parent mismatch")
	}
	if op == "post" || op == "mention" {
		a.root = receipt.ID
	}
	return nil
}

func (a *agent) wait(ctx context.Context, until time.Time) error {
	timer := time.NewTimer(time.Until(until))
	defer timer.Stop()
	var events <-chan irc.Event
	if a.alive {
		events = a.client.Events()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case e, ok := <-events:
			if !ok {
				a.alive = false
				events = nil
			} else {
				a.event(e)
			}
		}
	}
}

func (a *agent) runPhase(ctx context.Context, p phase) agentResult {
	a.live, a.mentions, a.gaps = 0, 0, 0
	period := time.Duration(float64(time.Second) / a.cfg.Rate)
	slots := int(p.duration / period)
	r := agentResult{samples: make([]sample, 0, slots)}
	for slot := 0; slot < slots; slot++ {
		offset := time.Duration(slot)*period + time.Duration(int64(period)*int64(a.index)/int64(a.total))
		due := p.start.Add(offset)
		err := a.wait(ctx, due)
		s := sample{Agent: a.index, Operation: mix[(slot+a.index)%len(mix)], Slot: slot, Scheduled: offset}
		switch {
		case err != nil:
			s.Outcome = "cancelled_before_start"
		case !a.alive:
			s.Outcome = "unavailable"
		case time.Now().Sub(due) >= period:
			s.Outcome = "missed_slot"
		default:
			start := time.Now()
			s.Started = start.Sub(p.start)
			err = a.operate(ctx, s.Operation, fmt.Sprintf("a%d-%t-%d", a.index, p.measured, slot))
			s.Elapsed = time.Since(start)
			s.Outcome = outcome(err)
			if err != nil {
				var rejected *irc.RejectedError
				if !errors.As(err, &rejected) {
					a.alive = false
					_ = a.client.Close()
				}
			}
		}
		r.samples = append(r.samples, s)
	}
	// Keep consuming fan-out until the phase's scheduled end plus a bounded drain.
	_ = a.wait(ctx, p.start.Add(p.duration+100*time.Millisecond))
	r.live, r.mentions, r.gaps = a.live, a.mentions, a.gaps
	return r
}

func runAgent(ctx context.Context, cfg config, address string, index, total int, commands <-chan phase, results chan<- agentResult) {
	a := agent{index: index, total: total, cfg: cfg, nick: fmt.Sprintf("load%04d", index), room: fmt.Sprintf("#load%d", index%cfg.Rooms)}
	start := time.Now()
	loginCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	c, err := irc.DialContext(loginCtx, irc.Config{Nick: a.nick, Addr: address, Ephemeral: true, WriteTimeout: cfg.Timeout})
	cancel()
	setup := agentResult{samples: []sample{{Agent: index, Operation: "login", Elapsed: time.Since(start), Outcome: outcome(err)}}}
	if err == nil {
		a.client, a.alive, setup.connected = c, true, true
		defer c.Close()
		stop := context.AfterFunc(ctx, func() { _ = c.Close() })
		defer stop()
		start = time.Now()
		err = c.Observe(a.room, "@"+a.nick)
		if err == nil {
			err = a.operate(ctx, "post", fmt.Sprintf("seed-%d", index))
		}
		setup.samples = append(setup.samples, sample{Agent: index, Operation: "subscribe_seed", Elapsed: time.Since(start), Outcome: outcome(err)})
		setup.ready = err == nil
		if err != nil {
			a.alive = false
			_ = c.Close()
		}
	}
	results <- setup
	for {
		var events <-chan irc.Event
		if a.alive {
			events = a.client.Events()
		}
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				a.alive = false
			} else {
				a.event(e)
			}
		case p, ok := <-commands:
			if !ok {
				return
			}
			r := a.runPhase(ctx, p)
			select {
			case results <- r:
			case <-ctx.Done():
				return
			}
		}
	}
}
