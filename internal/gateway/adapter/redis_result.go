package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// RedisResult is inside the existing signed response envelope. Connection-local
// subscription identifiers are absent: they confer no authority or replay identity.
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

// Ordered literal channels and target are hashed under a protocol-specific
// domain to fit the established ResponseScope route identifier grammar.
func redisResponseScope(realm, principal, binding string, route config.RedisRoute) auth.ResponseScope {
	sum := sha256.Sum256([]byte("redis-pubsub-response\x00" + route.RequestChannel + "\x00" + route.ResponseChannel + "\x00" + route.Target.Kind + "\x00" + route.Target.Name))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func redisResultMessage(sealer *auth.ResponseSealer, scope auth.ResponseScope, route config.RedisRoute, requestID string, result *dispatch.DetachedResult, failure *core.Failure, now time.Time) ([]byte, error) {
	if requestID == "" || len(requestID) > 64 || (result == nil) == (failure == nil) || !config.RedisChannel(route.RequestChannel) || !config.RedisChannel(route.ResponseChannel) || route.RequestChannel == route.ResponseChannel || scope.Principal != route.Principal || scope != redisResponseScope(scope.Realm, scope.Principal, scope.Binding, route) {
		return nil, rediswire.ErrFrame
	}
	if result != nil {
		if result.Version != 1 || !redisRequestOperation(result.Operation) {
			return nil, rediswire.ErrFrame
		}
		switch result.Operation {
		case core.Send:
			if result.Result == nil || !result.Result.Durable || result.Result.RequestID != requestID {
				return nil, rediswire.ErrFrame
			}
		case core.ContextRead:
			if result.Context == nil {
				return nil, rediswire.ErrFrame
			}
		case core.HistoryRead, core.Search:
			if result.Page == nil {
				return nil, rediswire.ErrFrame
			}
		}
	}
	response := RedisResult{Version: 1, Protocol: "redis/pubsub", RequestID: requestID, RequestChannel: route.RequestChannel, ResponseChannel: route.ResponseChannel, Target: route.Target, Result: result, Error: failure}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	token, err := sealer.Sign(scope, payload, now)
	if err != nil {
		return nil, err
	}
	if len(token) > rediswire.MaxBulk {
		return nil, auth.ErrResponseCapacity
	}
	return []byte(token), nil
}
