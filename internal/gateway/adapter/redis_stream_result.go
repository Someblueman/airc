package adapter

import (
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// RedisStreamResult is published as plain JSON on the route's result stream.
// EntryID correlates auto-ID entries; Redis consumer state and acknowledgement
// do not establish AIRC acceptance.
type RedisStreamResult struct {
	Version      int                      `json:"version"`
	Protocol     string                   `json:"protocol"`
	RequestID    string                   `json:"request_id"`
	Namespace    string                   `json:"namespace"`
	Stream       string                   `json:"stream"`
	ResultStream string                   `json:"result_stream"`
	EntryID      string                   `json:"entry_id"`
	Target       core.Target              `json:"target"`
	Result       *dispatch.DetachedResult `json:"result,omitempty"`
	Error        *core.Failure            `json:"error,omitempty"`
}

func redisStreamResultMessage(broker config.RedisStreams, entryID, requestID string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error) {
	if !rediswire.StreamEntryID(entryID) || !config.RedisChannel(broker.Route.Stream) || !config.RedisChannel(broker.Route.ResultStream) || broker.Route.Stream == broker.Route.ResultStream || !redisResultShape(requestID, result, failure) {
		return nil, rediswire.ErrFrame
	}
	return redisResultPayload(RedisStreamResult{Version: 1, Protocol: "redis/streams", RequestID: requestID, Namespace: broker.Namespace, Stream: broker.Route.Stream, ResultStream: broker.Route.ResultStream, EntryID: entryID, Target: broker.Route.Target, Result: result, Error: failure})
}
