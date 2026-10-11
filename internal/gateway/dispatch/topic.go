package dispatch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func (d *Dispatcher) readTopic(ctx context.Context, who actor, r core.Request, now time.Time) (core.TopicResult, error) {
	grant, err := d.authorizeRoomRead(ctx, who, r, core.TopicRead, now)
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
