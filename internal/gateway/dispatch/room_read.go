package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) authorizeRoomRead(ctx context.Context, who actor, r core.Request, operation core.Operation, now time.Time) (*store.ReadGrant, error) {
	if !d.enabled(who, operation) {
		return nil, ErrUnsupported
	}
	access, err := d.permissions(ctx, who)
	if err != nil {
		return nil, err
	}
	if r.Target == nil || r.Target.Kind != "room" || !slices.Contains(access.Operations, operation) || !access.allows(*r.Target) {
		return nil, auth.ErrForbidden
	}
	return d.authorizeRead(ctx, who, r, now)
}
