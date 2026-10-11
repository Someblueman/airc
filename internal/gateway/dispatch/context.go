package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

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
