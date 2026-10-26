package dispatch

import (
	"context"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func (d *Dispatcher) readAgents(ctx context.Context, who actor, r core.Request, now time.Time) (core.AgentsResult, error) {
	if !d.enabled(who, core.AgentsRead) {
		return core.AgentsResult{}, ErrUnsupported
	}
	grant, err := d.authorizeRead(ctx, who, r, now)
	if err != nil {
		return core.AgentsResult{}, err
	}
	result, err := d.backend.Agents(ctx, grant)
	if err != nil {
		return core.AgentsResult{}, err
	}
	return result, nil
}
