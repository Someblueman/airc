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

// admitRedisStream requires the signed request ID to bind the exact immutable
// stream entry before dispatch. Broker credentials and entry IDs grant no author
// authority. A refusal never supplies a correlated identity or acknowledges data.
func admitRedisStream(parent context.Context, d *dispatch.Dispatcher, broker config.RedisStreams, stream string, entry rediswire.StreamEntry, requests chan struct{}) redisAdmission {
	r := broker.Route
	id, err := config.RedisStreamRequestID(broker.Namespace, stream, entry.ID)
	if err != nil || d == nil || cap(requests) < 1 || stream != r.Stream || !config.RedisChannel(r.ResultStream) || stream == r.ResultStream || entry.Deleted || len(entry.Payload) == 0 || len(entry.Payload) > rediswire.MaxBulk {
		return redisAdmission{refused: true}
	}
	verified, err := d.AuthenticateDetached(string(entry.Payload), false, time.Now())
	if err != nil || verified.PrincipalID() != r.Principal || verified.RequestID() != id {
		return redisAdmission{refused: true}
	}
	target, ok := verified.Target()
	if !ok || target != r.Target || !slices.Contains(r.Operations, verified.Operation()) || !redisRequestOperation(verified.Operation()) {
		return redisAdmission{refused: true}
	}
	a := redisAdmission{requestID: id, binding: verified.Binding()}
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
