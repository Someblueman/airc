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
	binding   string
	result    *dispatch.DetachedResult
	failure   *core.Failure
	refused   bool // No authenticated authorized response scope exists yet.
}

// admitRedis derives identity solely from signed intent. Redis credentials,
// channel names and subscription counts never authenticate an AIRC publisher.
func admitRedis(parent context.Context, d *dispatch.Dispatcher, route config.RedisRoute, channel string, payload []byte, requests chan struct{}) redisAdmission {
	if d == nil || cap(requests) < 1 || len(payload) == 0 || len(payload) > rediswire.MaxBulk || channel != route.RequestChannel || !config.RedisChannel(channel) || !config.RedisChannel(route.ResponseChannel) || channel == route.ResponseChannel {
		return redisAdmission{refused: true}
	}
	verified, err := d.AuthenticateDetached(string(payload), false, time.Now())
	if err != nil || verified.PrincipalID() != route.Principal {
		return redisAdmission{refused: true}
	}
	target, ok := verified.Target()
	if !ok || target != route.Target || !slices.Contains(route.Operations, verified.Operation()) || !redisRequestOperation(verified.Operation()) {
		return redisAdmission{refused: true}
	}
	a := redisAdmission{requestID: verified.RequestID(), binding: verified.Binding()}
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
