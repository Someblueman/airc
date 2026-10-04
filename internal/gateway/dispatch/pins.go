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

// ReadPins is the bounded bearer pins.read profile. The backend consumes one
// room-scoped read grant and owns the native session lifecycle.
func (d *Dispatcher) ReadPins(ctx context.Context, token string, body []byte, now time.Time) (core.PinsResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.PinsResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.PinsRead {
		return core.PinsResult{}, store.ErrInvalid
	}
	return d.readPins(ctx, identity, r, now)
}

func (d *Dispatcher) readPins(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.PinsResult, error) {
	if !slices.Contains(d.operations, core.PinsRead) {
		return core.PinsResult{}, ErrUnsupported
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return core.PinsResult{}, err
	}
	if r.Target == nil || r.Target.Kind != "room" || !slices.Contains(access.Operations, core.PinsRead) || !slices.Contains(access.Targets, *r.Target) {
		return core.PinsResult{}, auth.ErrForbidden
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
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
