package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readPoll(ctx context.Context, who actor, r core.Request, now time.Time) (core.PollResult, error) {
	grant, err := d.authorizeRoomRead(ctx, who, r, core.PollResults, now)
	if err != nil {
		return core.PollResult{}, err
	}
	result, err := d.backend.Poll(ctx, grant)
	if err != nil {
		return core.PollResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxPollBytes {
		return core.PollResult{}, store.ErrCapacity
	}
	return result, nil
}
