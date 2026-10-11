package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) ReadWaiting(ctx context.Context, token string, body []byte, now time.Time) (core.WaitingResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.WaitingResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.WaitingRead {
		return core.WaitingResult{}, store.ErrInvalid
	}
	return d.readWaiting(ctx, principal(identity), r, now)
}

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
