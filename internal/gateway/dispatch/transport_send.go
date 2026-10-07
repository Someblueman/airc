package dispatch

import (
	"context"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"time"
)

// TransportSend admits a bounded send under an identity already authenticated by
// this dispatcher's verifier. Durable current authorization is checked by Admit.
func (d *Dispatcher) TransportSend(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.Result, error) {
	if d == nil || !d.verifier.OwnsIdentity(identity) {
		return core.Result{}, auth.ErrAuthentication
	}
	if r.Operation != core.Send {
		return core.Result{}, store.ErrInvalid
	}
	if err := r.Normalize(); err != nil {
		return core.Result{}, store.ErrInvalid
	}
	return d.execute(ctx, identity, r, 0, now)
}
