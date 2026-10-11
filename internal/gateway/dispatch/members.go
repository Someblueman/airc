package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readMembers(ctx context.Context, who actor, r core.Request, now time.Time) (core.MembersResult, error) {
	grant, err := d.authorizeRoomRead(ctx, who, r, core.MembersRead, now)
	if err != nil {
		return core.MembersResult{}, err
	}
	result, err := d.backend.Members(ctx, grant)
	if err != nil {
		return core.MembersResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxMembersBytes || len(result.Members) > core.MaxMembers {
		return core.MembersResult{}, store.ErrCapacity
	}
	return result, nil
}
