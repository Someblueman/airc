package backend

import (
	"context"
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
