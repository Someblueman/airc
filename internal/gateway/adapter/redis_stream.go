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
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// ServeRedisStreams runs one outbound owner of a preprovisioned group and stable
// consumer. Caller-owned configuration and passwords are snapshotted; owned
// credential bytes are cleared only after all socket work joins.
func ServeRedisStreams(parent context.Context, b config.RedisStreams, security *tls.Config, password []byte, realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, slots, requests chan struct{}) error {
	_, err := config.RedisStreamRequestID(b.Namespace, b.Route.Stream, "1-0")
	if err != nil || d == nil || sealer == nil || realm == "" || cap(requests) < 1 || !redisStreamInputs(b, security, password, slots) {
		return errRedisConnection
	}
	b.Route.Operations = slices.Clone(b.Route.Operations)
	security = security.Clone()
	password = slices.Clone(password)
	defer clear(password)
	backoff := redisBackoffMin
	for parent.Err() == nil {
		p, err := openRedisStream(parent, b, security, password, slots)
		if err == nil {
			if p.serveStreams(realm, d, sealer, requests) {
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

// Alternate one pending read with one new read. Keeping the pending cursor while
// advancing through refused/deleted entries avoids a poison entry starving new
// intake. Empty pending history resets the scan; reconnect starts it at zero.
func (p *redisStreamConn) serveStreams(realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, requests chan struct{}) bool {
	defer p.close()
	progress := false
	cursor := "0-0"
	for p.ctx.Err() == nil {
		for _, pending := range []bool{true, false} {
			position := ">"
			if pending {
				position = cursor
			}
			entry, err := p.next(position)
			if err != nil {
				return progress
			}
			if pending {
				if entry == nil {
					cursor = "0-0"
				} else {
					cursor = entry.ID
				}
			}
			if entry == nil {
				continue
			}
			completed, err := p.completeStreamEntry(realm, d, sealer, *entry, requests)
			if err != nil {
				return progress
			}
			progress = progress || completed
		}
	}
	return progress
}

func (p *redisStreamConn) completeStreamEntry(realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, entry rediswire.StreamEntry, requests chan struct{}) (bool, error) {
	a := admitRedisStream(p.ctx, d, p.broker, p.broker.Route.Stream, entry, requests)
	if a.refused {
		a = recoverRedisStream(p.ctx, d, p.broker, entry, requests)
	}
	if a.refused {
		return false, nil
	}
	return p.publishStreamAdmission(realm, sealer, entry.ID, a)
}

func (p *redisStreamConn) publishStreamAdmission(realm string, sealer *auth.ResponseSealer, entryID string, a redisAdmission) (bool, error) {
	scope := redisStreamResponseScope(realm, p.broker.Route.Principal, a.binding, p.broker, entryID)
	payload, err := redisStreamResultMessage(sealer, scope, p.broker, entryID, a.requestID, a.result, a.failure, time.Now())
	completed := a.result != nil
	if errors.Is(err, auth.ErrResponseCapacity) {
		completed = false // The whole read result was not delivered.
		payload, err = redisStreamResultMessage(sealer, scope, p.broker, entryID, a.requestID, nil, &core.Failure{Code: core.CapacityExceeded, Transient: true}, time.Now())
	}
	if err != nil {
		return false, err
	}
	if err = p.publish(payload); err != nil {
		return false, err
	}
	// Failures remain pending. A Send result must already prove durable journal
	// intake; bounded reads may be acknowledged only after their whole result.
	if !completed {
		return false, nil
	}
	if err = p.ack(entryID); err != nil {
		return false, err
	}
	return true, nil
}

// Recovery is lookup-only: an expired token cannot admit or reschedule a Send.
// It uses the same shared request capacity and deadline as fresh admission.
func recoverRedisStream(parent context.Context, d *dispatch.Dispatcher, b config.RedisStreams, entry rediswire.StreamEntry, requests chan struct{}) redisAdmission {
	id, err := config.RedisStreamRequestID(b.Namespace, b.Route.Stream, entry.ID)
	if err != nil || d == nil || cap(requests) < 1 || entry.Deleted || len(entry.Payload) < 1 || len(entry.Payload) > rediswire.MaxBulk || !slices.Contains(b.Route.Operations, core.Send) {
		return redisAdmission{refused: true}
	}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return redisAdmission{refused: true}
	}
	ctx, cancel := context.WithTimeout(parent, redisDeadline)
	defer cancel()
	result, binding, err := d.RetainedSignedSend(ctx, string(entry.Payload), b.Route.Principal, id, b.Route.Target, time.Now())
	if err != nil {
		return redisAdmission{refused: true}
	}
	return redisAdmission{requestID: id, binding: binding, result: &result}
}
