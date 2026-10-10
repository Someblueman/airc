package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) ReadPoll(ctx context.Context, token string, body []byte, now time.Time) (core.PollResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.PollResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.PollResults {
		return core.PollResult{}, store.ErrInvalid
	}
	return d.readPoll(ctx, identity, r, now)
}

func (d *Dispatcher) readPoll(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.PollResult, error) {
	grant, err := d.authorizeRoomRead(ctx, identity, r, core.PollResults, now)
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
