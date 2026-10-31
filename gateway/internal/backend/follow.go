package backend

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// FollowLease is how long one followed check delivers before its client
// resumes it, and FollowKeepAlive how often a quiet one sends an empty
// result; a zero lease means follows are refused.
func (b *Backend) FollowLease() time.Duration     { return b.followLease }
func (b *Backend) FollowKeepAlive() time.Duration { return b.followKeepAlive }

// FollowBatch is the shortest time between two snapshots of one follow:
// news arriving sooner waits for the next one, so a busy room costs a
// snapshot per interval rather than one per message. The first news after
// a quiet spell is read at once.
func (b *Backend) FollowBatch() time.Duration { return b.followBatch }

// Follow is Check as a stream: one session observes the check's rooms,
// threads and inbox for the follow lease and emits a result whenever news
// arrives, each page carrying the position the next page continues from,
// so a client holds one connection instead of checking every few seconds.
// The first result is the snapshot at the requested positions, news or
// not. A quiet follow emits an empty result every keepalive interval. The
// session slot is held only while a snapshot runs; between them the follow
// holds a wait slot, as a waiting check does. The lease ending is
// ErrStreamRenew: the client resumes from the last positions it received.
func (b *Backend) Follow(ctx context.Context, grant *store.ReadGrant, scope CheckScope, emit func(core.CheckResult) error) error {
	if b.followLease <= 0 || emit == nil {
		return ErrReadUnsupported
	}
	a, err := grant.Consume(time.Now())
	if err != nil {
		return err
	}
	r := a.Request
	if r.Operation != core.Check || len(r.Rooms) == 0 && len(r.Threads) == 0 && r.Inbox == nil {
		return store.ErrInvalid
	}
	scope.DirectMessages = scope.DirectMessages && a.Author.Registered
	deadline := time.Now().Add(b.followLease)
	return b.withReadOptions(ctx, a, readOptions{follow: true, deadline: deadline}, func(overall context.Context, client *irc.Client) error {
		if !client.Supports("CHECK") {
			return ErrReadUnsupported
		}
		p := checkPlan{request: r, scope: scope, nick: client.Nick()}
		if !strings.EqualFold(p.nick, a.Author.Nick) {
			return auth.ErrForbidden
		}
		if scope.DirectMessages {
			p.account = client.AccountID()
			if !validHistoryID(p.account) {
				return auth.ErrForbidden
			}
		}
		lane := a.Lane()
		if !b.acquireWait(lane) {
			return store.ErrCapacity
		}
		defer b.releaseWait(lane)
		defer func() {
			if unobserve(client, p.selectors()) != nil {
				_ = client.Close()
			}
		}()
		if _, err := observe(overall, client, p.selectors()); err != nil {
			return err
		}
		var woke bool
		news := func(event irc.Event) error {
			woke = woke || p.wakes(event)
			return nil
		}
		last := time.Now() // when the latest snapshot started
		result, err := p.snapshot(overall, client, news)
		if err != nil {
			return err
		}
		if err := emit(result); err != nil {
			return err
		}
		held := true // the session slot withReadOptions took
		// ended maps a wait that failed: the read's deferred release
		// returns a slot, so take it, and a lease that ran out is a
		// renewable end.
		ended := func(err error) error {
			b.reacquire(lane)
			if overall.Err() != nil && !time.Now().Before(deadline) {
				return ErrStreamRenew
			}
			return err
		}
		for {
			p.advance(result)
			if morePages(result) {
				// More was already waiting: read on before waiting.
				last = time.Now()
				if result, err = p.snapshot(overall, client, nil); err != nil {
					return err
				}
				if err := emit(result); err != nil {
					return err
				}
				continue
			}
			if held {
				b.release(lane, store.LookupAttempt)
				held = false
			}
			if !woke {
				if err := p.awaitFollow(overall, client, b.followKeepAlive, func() error { return emit(quiet(result)) }); err != nil {
					return ended(err)
				}
			}
			// Gather what else arrives until the batch interval since the
			// last snapshot has passed; it is all read in the next one.
			if wait := b.followBatch - time.Since(last); wait > 0 {
				if err := drainFor(overall, client, wait); err != nil {
					return ended(err)
				}
			}
			woke = false
			held = true
			if !b.reacquire(lane) {
				return store.ErrCapacity
			}
			last = time.Now()
			if result, err = p.snapshot(overall, client, nil); err != nil {
				return err
			}
			if err := emit(result); err != nil {
				return err
			}
		}
	})
}

// advance moves each selector's position past the page it just read, so
// the next snapshot starts after it. A page without a cursor (nothing
// retained after an expired start, or a retained read cut short before its
// first message) keeps its position.
func (p *checkPlan) advance(result core.CheckResult) {
	for i, page := range result.Rooms {
		if page.Cursor != "" {
			p.request.Rooms[i].After = page.Cursor
		}
	}
	for i, page := range result.Threads {
		if page.Cursor != "" {
			p.request.Threads[i].After = page.Cursor
		}
	}
	if result.Inbox != nil && result.Inbox.Cursor != "" {
		inbox := *p.request.Inbox
		inbox.After = result.Inbox.Cursor
		p.request.Inbox = &inbox
	}
}

// morePages reports a result with a page cut short: more is waiting.
func morePages(result core.CheckResult) bool {
	for _, page := range result.Rooms {
		if page.Status == "more" {
			return true
		}
	}
	for _, page := range result.Threads {
		if page.Status == "more" {
			return true
		}
	}
	return result.Inbox != nil && result.Inbox.Status == "more"
}

// quiet is result with its news removed: the positions a client continues
// from, as a keepalive.
func quiet(result core.CheckResult) core.CheckResult {
	clear := func(pages []core.CheckPage) []core.CheckPage {
		out := make([]core.CheckPage, len(pages))
		for i, page := range pages {
			out[i] = core.CheckPage{Room: page.Room, MentionsOnly: page.MentionsOnly, Messages: []*irc.MessageMetadata{}, Cursor: page.Cursor, Status: page.Status}
			if out[i].Status == "more" {
				out[i].Status = "ok"
			}
		}
		return out
	}
	q := core.CheckResult{Rooms: clear(result.Rooms)}
	if result.Threads != nil {
		q.Threads = clear(result.Threads)
	}
	if result.Inbox != nil {
		inbox := clear([]core.CheckPage{*result.Inbox})[0]
		q.Inbox = &inbox
	}
	return q
}

// drainFor reads and discards the session's events for wait: the news they
// announce is read by the snapshot that follows. A session that fails or
// is refused ends it.
func drainFor(ctx context.Context, client *irc.Client, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return ErrReadUnavailable
			}
			if raw, ok := event.(*irc.RawEvent); ok && rejectedRead(raw) {
				return ErrReadUnavailable
			}
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return errors.Join(ErrReadUnavailable, ctx.Err())
		}
	}
}

// awaitFollow waits for a live message for the check, sending keepalive
// frames every keepAlive (and pinging AIRC inside its read timeout), until
// one arrives, the session fails, or ctx ends.
func (p checkPlan) awaitFollow(ctx context.Context, client *irc.Client, keepAlive time.Duration, keep func() error) error {
	frames := time.NewTicker(keepAlive)
	defer frames.Stop()
	pings := time.NewTicker(checkKeepAlive)
	defer pings.Stop()
	for {
		select {
		case <-frames.C:
			if err := keep(); err != nil {
				return err
			}
		case <-pings.C:
			if err := client.Raw("PING :airc-gw-keepalive"); err != nil {
				return ErrReadUnavailable
			}
		case event, ok := <-client.Events():
			if !ok {
				return ErrReadUnavailable
			}
			if raw, ok := event.(*irc.RawEvent); ok && rejectedRead(raw) {
				return ErrReadUnavailable
			}
			if p.wakes(event) {
				return nil
			}
		case <-ctx.Done():
			return errors.Join(ErrReadUnavailable, ctx.Err())
		}
	}
}
