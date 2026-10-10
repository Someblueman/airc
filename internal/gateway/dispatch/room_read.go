package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) authorizeRoomRead(ctx context.Context, identity auth.Identity, r core.Request, operation core.Operation, now time.Time) (*store.ReadGrant, error) {
	if !slices.Contains(d.operations, operation) {
		return nil, ErrUnsupported
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return nil, err
	}
	if r.Target == nil || r.Target.Kind != "room" || !slices.Contains(access.Operations, operation) || !slices.Contains(access.Targets, *r.Target) {
		return nil, auth.ErrForbidden
	}
	return d.journal.AuthorizeRead(ctx, identity, r, now)
}
