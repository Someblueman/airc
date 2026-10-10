package adapter

import (
	"context"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type redisAdmission struct {
	requestID string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	refused   bool // No admitted request exists to correlate a result with.
}

// admitRedis attributes a plain contract request to the route's configured
// principal. The route, not the payload, carries authority: any Redis client
// whose ACL lets it publish on the request channel acts as that principal.
func admitRedis(parent context.Context, d *dispatch.Dispatcher, route config.RedisRoute, channel string, payload []byte, requests chan struct{}) redisAdmission {
	if channel != route.RequestChannel || !config.RedisChannel(channel) || !config.RedisChannel(route.ResponseChannel) || channel == route.ResponseChannel {
		return redisAdmission{refused: true}
	}
	return admitRedisRequest(parent, d, route.Principal, route.Target, route.Operations, payload, requests)
}

// admitRedisRequest refuses, without a correlated result, any request that is
// malformed, outside the route's target and operations, or whose principal is
// unknown or revoked in configuration. Admitted requests share request capacity.
func admitRedisRequest(parent context.Context, d *dispatch.Dispatcher, principal string, routeTarget core.Target, operations []core.Operation, payload []byte, requests chan struct{}) redisAdmission {
	if d == nil || cap(requests) < 1 || len(payload) == 0 || len(payload) > rediswire.MaxBulk {
		return redisAdmission{refused: true}
	}
	verified, err := d.AuthenticateRoute(principal, payload, time.Now())
	if err != nil {
		return redisAdmission{refused: true}
	}
	target, ok := verified.Target()
	if !ok || target != routeTarget || !slices.Contains(operations, verified.Operation()) || !redisRequestOperation(verified.Operation()) {
		return redisAdmission{refused: true}
	}
	a := redisAdmission{requestID: verified.RequestID()}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		a.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return a
	}
	ctx, cancel := context.WithTimeout(parent, redisDeadline)
	defer cancel()
	result, err := d.DetachedVerified(ctx, verified, time.Now())
	if err == nil && verified.Operation() == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		status, code := httpError(err)
		a.failure = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		a.result = &result
	}
	return a
}

func redisRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
