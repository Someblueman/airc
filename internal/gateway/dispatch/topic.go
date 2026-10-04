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

func (d *Dispatcher) ReadTopic(ctx context.Context, token string, body []byte, now time.Time) (core.TopicResult, error) {
	identity, err := d.verifier.Authenticate(token)
	if err != nil {
		return core.TopicResult{}, err
	}
	r, err := core.Decode(body)
	if err != nil || r.Operation != core.TopicRead {
		return core.TopicResult{}, store.ErrInvalid
	}
	return d.readTopic(ctx, identity, r, now)
}

func (d *Dispatcher) readTopic(ctx context.Context, identity auth.Identity, r core.Request, now time.Time) (core.TopicResult, error) {
	if !slices.Contains(d.operations, core.TopicRead) {
		return core.TopicResult{}, ErrUnsupported
	}
	access, err := d.journal.Permissions(ctx, identity)
	if err != nil {
		return core.TopicResult{}, err
	}
	if r.Target == nil || r.Target.Kind != "room" || !slices.Contains(access.Operations, core.TopicRead) || !slices.Contains(access.Targets, *r.Target) {
		return core.TopicResult{}, auth.ErrForbidden
	}
	grant, err := d.journal.AuthorizeRead(ctx, identity, r, now)
	if err != nil {
		return core.TopicResult{}, err
	}
	result, err := d.backend.Topic(ctx, grant)
	if err != nil {
		return core.TopicResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded)+1 > core.MaxTopicBytes {
		return core.TopicResult{}, store.ErrCapacity
	}
	return result, nil
}
