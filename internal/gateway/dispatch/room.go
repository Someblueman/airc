package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readRoom(ctx context.Context, who actor, r core.Request, now time.Time) (core.RoomResult, error) {
	grant, err := d.authorizeRoomRead(ctx, who, r, core.RoomRead, now)
	if err != nil {
		return core.RoomResult{}, err
	}
	result, err := d.backend.Room(ctx, grant)
	if err != nil {
		return core.RoomResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxRoomBytes {
		return core.RoomResult{}, store.ErrCapacity
	}
	return result, nil
}
