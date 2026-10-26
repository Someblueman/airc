package dispatch

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
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

// readRoom authorizes operation on the request's room and returns read's
// result when its JSON line fits within limit bytes.
func readRoom[T any](ctx context.Context, d *Dispatcher, who actor, r core.Request, operation core.Operation, now time.Time, read func(context.Context, *store.ReadGrant) (T, error), limit int) (T, error) {
	var zero T
	grant, err := d.authorizeRoomRead(ctx, who, r, operation, now)
	if err != nil {
		return zero, err
	}
	result, err := read(ctx, grant)
	if err != nil {
		return zero, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > limit {
		return zero, store.ErrCapacity
	}
	return result, nil
}
