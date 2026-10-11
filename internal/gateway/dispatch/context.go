package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ReadContext is the bounded bearer context.read profile. The backend method
// consumes exactly one read grant and owns the native session lifecycle.
func (d *Dispatcher) ReadContext(ctx context.Context, token string, body []byte, now time.Time) (core.ContextResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.ContextResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.ContextRead {
		return core.ContextResult{}, store.ErrInvalid
	}
	return d.readContext(ctx, principal(identity), r, now)
}

func (d *Dispatcher) readContext(ctx context.Context, who actor, r core.Request, now time.Time) (core.ContextResult, error) {
	if !d.enabled(who, r.Operation) {
		return core.ContextResult{}, ErrUnsupported
	}
	access, err := d.permissions(ctx, who)
	if err != nil {
		return core.ContextResult{}, err
	}
	if !slices.Contains(access.Operations, r.Operation) || r.Target == nil || !access.allows(*r.Target) {
		return core.ContextResult{}, auth.ErrForbidden
	}
	grant, err := d.authorizeRead(ctx, who, r, now)
	if err != nil {
		return core.ContextResult{}, err
	}
	return d.backend.Context(ctx, grant)
}
