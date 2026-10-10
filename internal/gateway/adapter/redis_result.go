package adapter

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// errRedisResultCapacity reports a whole result that does not fit one bulk
// string. Callers publish a transient capacity failure instead of truncating.
var errRedisResultCapacity = errors.New("redis result exceeds bulk capacity")

// RedisResult is published as plain JSON on the route's response channel.
// Connection-local subscription identifiers are absent: they carry no identity.
type RedisResult struct {
	Version         int                      `json:"version"`
	Protocol        string                   `json:"protocol"`
	RequestID       string                   `json:"request_id"`
	RequestChannel  string                   `json:"request_channel"`
	ResponseChannel string                   `json:"response_channel"`
	Target          core.Target              `json:"target"`
	Result          *dispatch.DetachedResult `json:"result,omitempty"`
	Error           *core.Failure            `json:"error,omitempty"`
}

func redisResultMessage(route config.RedisRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure) ([]byte, error) {
	if !config.RedisChannel(route.RequestChannel) || !config.RedisChannel(route.ResponseChannel) || route.RequestChannel == route.ResponseChannel || !redisResultShape(requestID, result, failure) {
		return nil, rediswire.ErrFrame
	}
	return redisResultPayload(RedisResult{Version: 1, Protocol: "redis/pubsub", RequestID: requestID, RequestChannel: route.RequestChannel, ResponseChannel: route.ResponseChannel, Target: route.Target, Result: result, Error: failure})
}

// redisResultShape requires exactly one whole result or failure for the request.
func redisResultShape(requestID string, result *dispatch.DetachedResult, failure *core.Failure) bool {
	if requestID == "" || len(requestID) > 64 || (result == nil) == (failure == nil) {
		return false
	}
	if result == nil {
		return true
	}
	if result.Version != 1 || !redisRequestOperation(result.Operation) {
		return false
	}
	switch result.Operation {
	case core.Send:
		return result.Result != nil && result.Result.Durable && result.Result.RequestID == requestID
	case core.ContextRead:
		return result.Context != nil
	default:
		return result.Page != nil
	}
}

func redisResultPayload(response any) ([]byte, error) {
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if len(payload) > rediswire.MaxBulk {
		return nil, errRedisResultCapacity
	}
	return payload, nil
}
