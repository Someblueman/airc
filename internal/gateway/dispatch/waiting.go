package dispatch

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
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
	return d.readWaiting(ctx, identity, r, now)
}

func (d *Dispatcher) readWaiting(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.WaitingResult, error) {
	if !slices.Contains(d.operations, core.WaitingRead) {
		return core.WaitingResult{}, ErrUnsupported
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return core.WaitingResult{}, err
	}
	if r.Target == nil || r.Target.Kind != "room" || !slices.Contains(access.Operations, core.WaitingRead) || !slices.Contains(access.Targets, *r.Target) {
		return core.WaitingResult{}, auth.ErrForbidden
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
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
