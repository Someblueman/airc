package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

const redisBackoffMin = 100 * time.Millisecond
const redisBackoffMax = 5 * time.Second

// ServeRedisPubSub runs one outbound broker owner. Callers supply validated
// configuration and private credential bytes. All inputs are snapshotted; owned
// password copies are cleared on return. This loop cannot recover pre-intake loss.
func ServeRedisPubSub(parent context.Context, b config.RedisPubSub, security *tls.Config, subscriberPassword, publisherPassword []byte, realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, slots, requests chan struct{}) error {
	if d == nil || sealer == nil || realm == "" || cap(requests) < 1 || !redisPairInputs(b, security, subscriberPassword, publisherPassword, slots) {
		return errRedisConnection
	}
	b.Routes = slices.Clone(b.Routes)
	for i := range b.Routes {
		b.Routes[i].Operations = slices.Clone(b.Routes[i].Operations)
	}
	security = security.Clone()
	subscriberPassword, publisherPassword = slices.Clone(subscriberPassword), slices.Clone(publisherPassword)
	defer clear(subscriberPassword)
	defer clear(publisherPassword)
	backoff := redisBackoffMin
	for parent.Err() == nil {
		p, err := openRedisPair(parent, b, security, subscriberPassword, publisherPassword, slots)
		if err == nil {
			if p.serve(realm, d, sealer, requests) {
				backoff = redisBackoffMin
			}
			p.close()
		}
		if !redisWait(parent, backoff) {
			break
		}
		backoff = min(backoff*2, redisBackoffMax)
	}
	return nil
}

func redisWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

// serve completes each admitted operation and result before reading another
// publication. Invalid signed intent is dropped without a correlated response;
// it does not disconnect every publisher sharing the broker subscription.
func (p *redisPair) serve(realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, requests chan struct{}) bool {
	defer p.close()
	progress := false
	for p.ctx.Err() == nil {
		route, body, err := p.next()
		if err != nil {
			break
		}
		a := admitRedis(p.ctx, d, route, route.RequestChannel, body, requests)
		if a.refused {
			continue
		}
		scope := redisResponseScope(realm, route.Principal, a.binding, route)
		payload, err := redisResultMessage(sealer, scope, route, a.requestID, a.result, a.failure, time.Now())
		if errors.Is(err, auth.ErrResponseCapacity) {
			payload, err = redisResultMessage(sealer, scope, route, a.requestID, nil, &core.Failure{Code: core.CapacityExceeded, Transient: true}, time.Now())
		}
		if err != nil || p.publish(route.ResponseChannel, payload) != nil {
			break
		}
		progress = true
	}
	return progress
}
