package backend

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// withRead owns the admitted SDK session, cancellation and pool release. Both
// readers consume their one-use grant before invoking this private boundary.
func (b *Backend) withRead(ctx context.Context, a store.ReadAttempt, stream bool, read func(context.Context, *irc.Client) error) error {
	return b.withReadOptions(ctx, a, readOptions{stream: stream, deadline: a.Deadline}, read)
}

// readOptions shape one read session: stream is a watch's session, left
// observing and read until deadline; follow is a followed check's, kept
// observing for its own lease (deadline) past the grant's, so never pooled.
type readOptions struct {
	stream, follow bool
	deadline       time.Time
}

func (b *Backend) withReadOptions(ctx context.Context, a store.ReadAttempt, opts readOptions, read func(context.Context, *irc.Client) error) error {
	clientConfig, ok := b.sessionConfig(a.Backend, a.Author, a.Credential, a.Source)
	if !ok || a.Realm != b.realm || a.Author.Scope() != a.Scope {
		return auth.ErrForbidden
	}
	lane := a.Lane()
	if a.Request.Operation.IsWatch() {
		if !b.acquireWatch(lane) {
			return store.ErrCapacity
		}
		defer b.releaseWatch(lane)
	} else {
		if !b.acquireRead(ctx, lane) {
			return store.ErrCapacity
		}
		defer b.release(lane, store.LookupAttempt)
	}
	stream := opts.stream
	overall, cancel := context.WithDeadline(ctx, opts.deadline)
	defer cancel()
	dialDeadline := time.Now().Add(10 * time.Second)
	if stream {
		dialDeadline = a.Deadline.Add(-store.ReadLeaseDuration + 4*time.Second)
	}
	dialCtx, stopDial := context.WithDeadline(overall, dialDeadline)
	if stream {
		clientConfig.ReadTimeout = store.ReadLeaseDuration
	}
	// A watch or a follow leaves its session observing, so only reads that
	// are neither take and keep pooled sessions.
	pooled := !stream && !opts.follow && !a.Request.Operation.IsWatch()
	key := sessionKey(clientConfig, a.Scope)
	var client *irc.Client
	var dialed time.Time
	var err error
	if pooled {
		client, dialed = b.pool.take(key, clientConfig.Nick, !a.Author.Registered)
	}
	if client == nil {
		client, err = irc.DialContext(dialCtx, clientConfig)
		dialed = time.Now()
	}
	stopDial()
	if err != nil {
		// AIRC's refusal of the author (a wrong token, a guest on a
		// registered nickname, a nickname in use) is the read's answer; an
		// unreachable upstream stays a transient read failure.
		var refusal *Refusal
		if errors.As(accountRefusal(a.Author, false, err), &refusal) && refusal.Failure.Code != core.UpstreamUnavailable {
			return refusal
		}
		return ErrReadUnavailable
	}
	closed := make(chan struct{})
	stopClose := context.AfterFunc(overall, func() { _ = client.Close(); close(closed) })
	reuse := false
	defer func() {
		if !stopClose() {
			<-closed
		} else if reuse {
			b.pool.put(key, client, dialed)
			return
		}
		_ = client.Close()
	}()
	err = read(overall, client)
	// Only a read that finished is kept: a refused or abandoned one may
	// leave replies unread.
	reuse = err == nil && pooled
	return err
}

// roomRead accepts a request for operation on a room.
func roomRead(operation core.Operation) func(core.Request) bool {
	return func(r core.Request) bool {
		return r.Operation == operation && r.Target != nil && r.Target.Kind == "room"
	}
}

// readSnapshot consumes grant for a valid request and fetches one bounded
// snapshot on an admitted read session. The snapshot must encode within limit
// bytes and complete before the read's deadline.
func readSnapshot[T any](ctx context.Context, b *Backend, grant *store.ReadGrant, valid func(core.Request) bool, limit int, fetch func(context.Context, *irc.Client, core.Request) (T, error)) (T, error) {
	var result, zero T
	a, err := grant.Consume(time.Now())
	if err != nil {
		return zero, err
	}
	if !valid(a.Request) {
		return zero, store.ErrInvalid
	}
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		candidate, err := fetch(overall, client, a.Request)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(candidate)
		if err != nil || len(encoded) > limit {
			return store.ErrCapacity
		}
		deadline, _ := overall.Deadline()
		if overall.Err() != nil || !time.Now().Before(deadline) {
			return ErrReadUnavailable
		}
		result = candidate
		return nil
	})
	if err != nil {
		return zero, err
	}
	return result, nil
}
