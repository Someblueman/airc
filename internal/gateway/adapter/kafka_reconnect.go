package adapter

import (
	"context"
	"crypto/tls"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

const kafkaBackoffMin = 100 * time.Millisecond
const kafkaBackoffMax = 5 * time.Second

// runKafkaConnections owns reconnect and joins every pair before waiting. The
// serial session callback handles durable records and returns whether it made
// verified progress; merely authenticating does not reset failure backoff.
func runKafkaConnections(ctx context.Context, b config.Kafka, security map[int32]*tls.Config, password []byte, slots chan struct{}, session func(*kafkaPair) bool) error {
	if session == nil || len(b.Brokers) < 1 || len(b.Brokers) > 32 || len(security) != len(b.Brokers) || cap(slots) < 1 || cap(slots) > 256 {
		return errKafkaConnection
	}
	body, err := kafkawire.PlainAuthenticate(b.Credential.Username, password)
	if err != nil {
		return errKafkaConnection
	}
	clear(body)
	b.Brokers = slices.Clone(b.Brokers)
	b.Route.Operations = slices.Clone(b.Route.Operations)
	if b.Route.InitialOffset != nil {
		offset := *b.Route.InitialOffset
		b.Route.InitialOffset = &offset
	}
	owned := make(map[int32]*tls.Config, len(security))
	for _, broker := range b.Brokers {
		s := security[broker.ID]
		if s == nil || s.InsecureSkipVerify || s.ServerName == "" || s.ServerName != broker.TLSServerName || owned[broker.ID] != nil {
			return errKafkaConnection
		}
		owned[broker.ID] = s.Clone()
	}
	password = slices.Clone(password)
	defer clear(password)
	backoff, seed := kafkaBackoffMin, 0
	for ctx.Err() == nil {
		p, err := openKafkaPair(ctx, b, owned, password, slots, seed)
		seed = (seed + 1) % len(b.Brokers)
		if err == nil {
			if ctx.Err() == nil && session(p) {
				backoff = kafkaBackoffMin
			}
			p.close()
		}
		if !kafkaWait(ctx, backoff) {
			break
		}
		backoff = min(backoff*2, kafkaBackoffMax)
	}
	return nil
}

func kafkaWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
