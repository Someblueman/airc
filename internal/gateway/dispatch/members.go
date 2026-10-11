package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readMembers(ctx context.Context, who actor, r core.Request, now time.Time) (core.MembersResult, error) {
	result, err := readRoom(ctx, d, who, r, core.MembersRead, now, d.backend.Members, core.MaxMembersBytes)
	if err == nil && len(result.Members) > core.MaxMembers {
		return core.MembersResult{}, store.ErrCapacity
	}
	return result, err
}
