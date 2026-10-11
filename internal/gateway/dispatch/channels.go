package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ReadChannels is the authenticated global room catalogue read. It consumes
// one read grant and never creates a durable job or receipt.
func (d *Dispatcher) ReadChannels(ctx context.Context, token string, body []byte, now time.Time) (core.ChannelsResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.ChannelsResult{}, err
	}
	request, err := core.Decode(body)
	if err != nil || request.Operation != core.ChannelsRead {
		return core.ChannelsResult{}, store.ErrInvalid
	}
	return d.readChannels(ctx, principal(identity), request, now)
}

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
