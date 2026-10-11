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

// admitRedis runs a plain contract request published on a route's request
// channel as the author it names, else the route's bound author. The Redis
// ACL on the channel, not the payload, decides who may publish; AIRC decides
// whether the author may post.
func admitRedis(parent context.Context, d *dispatch.Dispatcher, author dispatch.RouteAuthor, source string, route config.RedisRoute, channel string, payload []byte, requests chan struct{}) redisAdmission {
	if channel != route.RequestChannel || !config.RedisChannel(channel) || !config.RedisChannel(route.ResponseChannel) || channel == route.ResponseChannel {
		return redisAdmission{refused: true}
	}
	return admitRedisRequest(parent, d, author, source, route.Target, route.Operations, payload, requests)
}

// admitRedisRequest refuses, without a correlated result, any payload that is
// not a contract request. A request outside the route's target and operations
// gets a correlated forbidden failure. Admitted requests share request capacity.
func admitRedisRequest(parent context.Context, d *dispatch.Dispatcher, author dispatch.RouteAuthor, source string, routeTarget core.Target, operations []core.Operation, payload []byte, requests chan struct{}) redisAdmission {
	if d == nil || cap(requests) < 1 || len(payload) == 0 || len(payload) > rediswire.MaxBulk {
		return redisAdmission{refused: true}
	}
	sub, err := core.DecodeSubmission(payload)
	if err != nil || sub.Request.RequestID == "" || len(sub.Request.RequestID) > 64 {
		return redisAdmission{refused: true}
	}
	a := redisAdmission{requestID: sub.Request.RequestID}
	op := sub.Request.Operation
	if !routeTargetMatches(sub.Request, routeTarget) || !slices.Contains(operations, op) || !redisRequestOperation(op) {
		a.failure = &core.Failure{Code: core.Forbidden}
		return a
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		a.failure = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		return a
	}
	ctx, cancel := context.WithTimeout(parent, redisDeadline)
	defer cancel()
	result, err := d.SubmitRouteDecoded(ctx, author, source, sub, time.Now())
	if err == nil && op == core.Send && (result.Result == nil || !result.Result.Durable) {
		err = store.ErrInvalid
	}
	if err != nil {
		failure := errorFailureOf(err)
		a.failure = &failure
	} else {
		a.result = &result
	}
	return a
}

// routeTargetMatches reports whether a request names exactly a route's target.
func routeTargetMatches(r core.Request, target core.Target) bool {
	if r.Target == nil {
		return false
	}
	named := *r.Target
	return named.Normalize() == nil && named == target
}

// errorFailureOf maps an admission error to its stable failure, keeping
// AIRC's own refusal for synchronous account calls.
func errorFailureOf(err error) core.Failure {
	_, failure := errorFailure(err)
	return failure
}

func redisRequestOperation(op core.Operation) bool {
	return op == core.Send || op == core.ContextRead || op == core.HistoryRead || op == core.Search
}
