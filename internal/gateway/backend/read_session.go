package backend

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// withRead owns the admitted SDK session, cancellation and pool release. Both
// readers consume their one-use grant before invoking this private boundary.
func (b *Backend) withRead(ctx context.Context, a store.ReadAttempt, stream bool, read func(context.Context, *irc.Client) error) error {
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
		if !b.acquire(lane, store.LookupAttempt) {
			return store.ErrCapacity
		}
		defer b.release(lane, store.LookupAttempt)
	}
	overall, cancel := context.WithDeadline(ctx, a.Deadline)
	defer cancel()
	dialDeadline := time.Now().Add(10 * time.Second)
	if stream {
		dialDeadline = a.Deadline.Add(-store.ReadLeaseDuration + 4*time.Second)
	}
	dialCtx, stopDial := context.WithDeadline(overall, dialDeadline)
	if stream {
		clientConfig.ReadTimeout = store.ReadLeaseDuration
	}
	client, err := irc.DialContext(dialCtx, clientConfig)
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
	defer func() {
		if !stopClose() {
			<-closed
		}
		_ = client.Close()
	}()
	if !client.Ephemeral() {
		return ErrReadUnsupported
	}
	return read(overall, client)
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
