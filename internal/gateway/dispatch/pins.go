package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readPins(ctx context.Context, who actor, r core.Request, now time.Time) (core.PinsResult, error) {
	grant, err := d.authorizeRoomRead(ctx, who, r, core.PinsRead, now)
	if err != nil {
		return core.PinsResult{}, err
	}
	result, err := d.backend.Pins(ctx, grant)
	if err != nil {
		return core.PinsResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxPinsBytes {
		return core.PinsResult{}, store.ErrCapacity
	}
	return result, nil
}
