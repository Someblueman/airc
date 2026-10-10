package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) ReadRoom(ctx context.Context, token string, body []byte, now time.Time) (core.RoomResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.RoomResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.RoomRead {
		return core.RoomResult{}, store.ErrInvalid
	}
	return d.readRoom(ctx, identity, r, now)
}

func (d *Dispatcher) readRoom(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.RoomResult, error) {
	grant, err := d.authorizeRoomRead(ctx, identity, r, core.RoomRead, now)
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
