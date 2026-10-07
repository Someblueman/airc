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

// RedisStreamResult binds a retained entry to a whole signed dispatcher result.
// Redis consumer state and broker acknowledgement do not establish AIRC acceptance.
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

// Namespace, ordered streams, entry and target are hashed under a protocol-specific
// domain to fit the established ResponseScope route identifier grammar.
func redisStreamResponseScope(realm, principal, binding string, broker config.RedisStreams, entryID string) auth.ResponseScope {
	sum := sha256.Sum256([]byte("redis-stream-response\x00" + broker.Namespace + "\x00" + broker.Route.Stream + "\x00" + broker.Route.ResultStream + "\x00" + entryID + "\x00" + broker.Route.Target.Kind + "\x00" + broker.Route.Target.Name))
	return auth.ResponseScope{Realm: realm, Principal: principal, Binding: binding, Route: hex.EncodeToString(sum[:])}
}

func redisStreamResultMessage(sealer *auth.ResponseSealer, scope auth.ResponseScope, broker config.RedisStreams, entryID, requestID string, result *dispatch.DetachedResult, failure *core.Failure, now time.Time) ([]byte, error) {
	expectedID, err := config.RedisStreamRequestID(broker.Namespace, broker.Route.Stream, entryID)
	if err != nil || requestID != expectedID || (result == nil) == (failure == nil) || !config.RedisChannel(broker.Route.ResultStream) || broker.Route.Stream == broker.Route.ResultStream || scope.Principal != broker.Route.Principal || scope != redisStreamResponseScope(scope.Realm, scope.Principal, scope.Binding, broker, entryID) {
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
	response := RedisStreamResult{Version: 1, Protocol: "redis/streams", RequestID: requestID, Namespace: broker.Namespace, Stream: broker.Route.Stream, ResultStream: broker.Route.ResultStream, EntryID: entryID, Target: broker.Route.Target, Result: result, Error: failure}
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
