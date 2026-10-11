package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

const redisBackoffMin = 100 * time.Millisecond
const redisBackoffMax = 5 * time.Second

// ServeRedisPubSub runs one outbound broker owner. Callers supply validated
// configuration and private credential bytes. All inputs are snapshotted; owned
// password copies are cleared on return. This loop cannot recover pre-intake loss.
// authors holds each route's author policy, keyed by its request channel.
func ServeRedisPubSub(parent context.Context, b config.RedisPubSub, security *tls.Config, subscriberPassword, publisherPassword []byte, d *dispatch.Dispatcher, authors map[string]dispatch.RouteAuthor, slots, requests chan struct{}) error {
	if d == nil || cap(requests) < 1 || !redisPairInputs(b, security, subscriberPassword, publisherPassword, slots) {
		return errRedisConnection
	}
	for _, r := range b.Routes {
		if _, ok := authors[r.RequestChannel]; !ok {
			return errRedisConnection
		}
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
			if p.serve(d, authors, dispatch.RouteSource(b.ID), requests) {
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
// publication. Refused requests are dropped without a correlated response; they
// do not disconnect every publisher sharing the broker subscription.
func (p *redisPair) serve(d *dispatch.Dispatcher, authors map[string]dispatch.RouteAuthor, source string, requests chan struct{}) bool {
	defer p.close()
	progress := false
	for p.ctx.Err() == nil {
		route, body, err := p.next()
		if err != nil {
			break
		}
		a := admitRedis(p.ctx, d, authors[route.RequestChannel], source, route, route.RequestChannel, body, requests)
		if a.refused {
			continue
		}
		payload, err := redisResultMessage(route, a.requestID, a.result, a.failure)
		if errors.Is(err, errRedisResultCapacity) {
			payload, err = redisResultMessage(route, a.requestID, nil, &core.Failure{Code: core.CapacityExceeded, Transient: true})
		}
		if err != nil || p.publish(route.ResponseChannel, payload) != nil {
			break
		}
		progress = true
	}
	return progress
}
