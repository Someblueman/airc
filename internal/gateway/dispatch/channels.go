package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readChannels(ctx context.Context, who actor, request core.Request, now time.Time) (core.ChannelsResult, error) {
	if !d.enabled(who, core.ChannelsRead) {
		return core.ChannelsResult{}, ErrUnsupported
	}
	grant, err := d.authorizeRead(ctx, who, request, now)
	if err != nil {
		return core.ChannelsResult{}, err
	}
	result, err := d.backend.Channels(ctx, grant)
	if err != nil {
		return core.ChannelsResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxChannelsBytes || len(result.Channels) > core.MaxChannels {
		return core.ChannelsResult{}, store.ErrCapacity
	}
	return result, nil
}
