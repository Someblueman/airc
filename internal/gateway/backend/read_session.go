package backend

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// withRead owns the admitted SDK session, cancellation and pool release. Both
// readers consume their one-use grant before invoking this private boundary.
func (b *Backend) withRead(ctx context.Context, a store.ReadAttempt, stream bool, read func(context.Context, *irc.Client) error) error {
	entry, ok := b.bindings[a.Binding]
	if !ok || a.Realm != b.realm || b.principals[a.Principal] != a.Binding || entry.pinned != a.Backend {
		return auth.ErrForbidden
	}
	if a.Request.Operation == core.Watch {
		if !b.acquireWatch(a.Principal) {
			return store.ErrCapacity
		}
		defer b.releaseWatch(a.Principal)
	} else {
		if !b.acquire(a.Principal, store.LookupAttempt) {
			return store.ErrCapacity
		}
		defer b.release(a.Principal, store.LookupAttempt)
	}
	overall, cancel := context.WithDeadline(ctx, a.Deadline)
	defer cancel()
	dialDeadline := time.Now().Add(10 * time.Second)
	if stream {
		dialDeadline = a.Deadline.Add(-store.ReadLeaseDuration + 4*time.Second)
	}
	dialCtx, stopDial := context.WithDeadline(overall, dialDeadline)
	clientConfig := entry.client
	if stream {
		clientConfig.ReadTimeout = store.ReadLeaseDuration
	}
	client, err := irc.DialContext(dialCtx, clientConfig)
	stopDial()
	if err != nil {
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
