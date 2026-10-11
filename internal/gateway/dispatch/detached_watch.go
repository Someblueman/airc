package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// DetachedWatch authenticates each stream renewal, including its opaque cursor.
// Its one-shot read lease cannot extend beyond the envelope's validity boundary.
func (d *Dispatcher) DetachedWatch(ctx context.Context, token string, encrypted bool, now time.Time, emit func(backend.HistoryPage) error) error {
	verified, err := d.AuthenticateDetached(token, encrypted, now)
	if err != nil {
		return err
	}
	return d.DetachedWatchVerified(ctx, verified, now, emit)
}

// DetachedWatchVerified retains the original validity boundary and normalized
// cursor while avoiding a second signature/decryption step after route selection.
func (d *Dispatcher) DetachedWatchVerified(ctx context.Context, verified VerifiedMessage, now time.Time, emit func(backend.HistoryPage) error) error {
	message, err := verified.forDispatcher(d, now)
	if err != nil {
		return err
	}
	if !message.Request.Operation.IsWatch() || emit == nil {
		return store.ErrInvalid
	}
	readCtx, cancel := context.WithDeadline(ctx, time.Unix(message.ReplayUntil, 0))
	defer cancel()
	_, err = d.readPage(readCtx, principal(message.Identity), message.Request, "", now, emit)
	if readCtx.Err() == context.DeadlineExceeded {
		return backend.ErrStreamRenew
	}
	return err
}
