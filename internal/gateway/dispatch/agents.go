package dispatch

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) ReadAgents(ctx context.Context, token string, body []byte, now time.Time) (core.AgentsResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.AgentsResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.AgentsRead {
		return core.AgentsResult{}, store.ErrInvalid
	}
	return d.readAgents(ctx, identity, r, now)
}

func (d *Dispatcher) readAgents(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.AgentsResult, error) {
	if !slices.Contains(d.operations, core.AgentsRead) {
		return core.AgentsResult{}, ErrUnsupported
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
	if err != nil {
		return core.AgentsResult{}, err
	}
	result, err := d.backend.Agents(ctx, grant)
	if err != nil {
		return core.AgentsResult{}, err
	}
	return result, nil
}
