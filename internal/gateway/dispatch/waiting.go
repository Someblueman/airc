package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readWaiting(ctx context.Context, who actor, r core.Request, now time.Time) (core.WaitingResult, error) {
	grant, err := d.authorizeRoomRead(ctx, who, r, core.WaitingRead, now)
	if err != nil {
		return core.WaitingResult{}, err
	}
	result, err := d.backend.Waiting(ctx, grant)
	if err != nil {
		return core.WaitingResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxWaitingBytes {
		return core.WaitingResult{}, store.ErrCapacity
	}
	return result, nil
}
