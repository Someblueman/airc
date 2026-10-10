package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// ServeRedisStreams runs one outbound owner of a preprovisioned group and stable
// consumer. Caller-owned configuration and passwords are snapshotted; owned
// credential bytes are cleared only after all socket work joins.
func ServeRedisStreams(parent context.Context, b config.RedisStreams, security *tls.Config, password []byte, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if d == nil || cap(requests) < 1 || !redisStreamInputs(b, security, password, slots) {
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
			if p.serveStreams(d, requests) {
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
func (p *redisStreamConn) serveStreams(d *dispatch.Dispatcher, requests chan struct{}) bool {
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
			completed, err := p.completeStreamEntry(d, *entry, requests)
			if err != nil {
				return progress
			}
			progress = progress || completed
		}
	}
	return progress
}

// completeStreamEntry leaves refused entries pending without a result. A
// redelivered pending entry is dispatched again; the journal deduplicates its
// request_id, so a durable Send is never admitted twice.
func (p *redisStreamConn) completeStreamEntry(d *dispatch.Dispatcher, entry rediswire.StreamEntry, requests chan struct{}) (bool, error) {
	a := admitRedisStream(p.ctx, d, p.broker, p.broker.Route.Stream, entry, requests)
	if a.refused {
		return false, nil
	}
	return p.publishStreamAdmission(entry.ID, a)
}

func (p *redisStreamConn) publishStreamAdmission(entryID string, a redisAdmission) (bool, error) {
	payload, err := redisStreamResultMessage(p.broker, entryID, a.requestID, a.result, a.failure)
	// A whole result or a permanent failure completes the entry. Transient
	// failures stay pending for retry; plain requests do not expire, so a
	// permanent failure left pending would be retried and reported forever.
	completed := a.result != nil || a.failure != nil && !a.failure.Transient
	if errors.Is(err, errRedisResultCapacity) {
		completed = false // The whole read result was not delivered.
		payload, err = redisStreamResultMessage(p.broker, entryID, a.requestID, nil, &core.Failure{Code: core.CapacityExceeded, Transient: true})
	}
	if err != nil {
		return false, err
	}
	if err = p.publish(payload); err != nil {
		return false, err
	}
	// A Send result must already prove durable journal intake; bounded reads
	// may be acknowledged only after their whole result.
	if !completed {
		return false, nil
	}
	if err = p.ack(entryID); err != nil {
		return false, err
	}
	return true, nil
}
