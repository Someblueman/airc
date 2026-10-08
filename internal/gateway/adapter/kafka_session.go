package adapter

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// runKafkaSession is the pair's serial command owner. It returns only durable
// progress to the reconnect loop; authentication or an empty poll is not
// progress. The reconnect owner closes and joins the pair after this returns.
func runKafkaSession(ctx context.Context, p *kafkaPair, realm string, b config.Kafka, d *dispatch.Dispatcher, journal *store.Store, sealer *auth.ResponseSealer, requests chan struct{}) bool {
	if p == nil || p.request == nil || journal == nil || d == nil || sealer == nil || realm == "" || cap(requests) < 1 {
		return false
	}
	readCtx, cancel := context.WithTimeout(ctx, kafkaDeadline)
	position, err := journal.ProvisionKafka(readCtx, b.ID)
	cancel()
	if err != nil || p.verifyKafkaContinuity(b, position) != nil {
		return false
	}
	progress := false
	for ctx.Err() == nil {
		body, err := kafkawire.Fetch(b.Route.Topic, b.Route.Partition, p.leaders.Request.Epoch, position.NextOffset)
		if err != nil {
			return progress
		}
		reply, err := p.request.command(1, 11, body, time.Now().Add(kafkaDeadline))
		if err != nil {
			return progress
		}
		fetched, err := kafkawire.FetchReply(reply, b.Route.Topic, b.Route.Partition)
		if err != nil {
			return progress
		}
		next, err := p.processKafkaRange(ctx, realm, b, d, journal, sealer, requests, position, fetched)
		advanced := next.NextOffset > position.NextOffset
		progress = progress || advanced
		position = next
		if err != nil {
			return progress
		}
		// Brokers may return empty polls immediately. Bound this idle path without
		// delaying returned records or manufacturing offset progress.
		if !advanced && !kafkaWait(ctx, kafkaBackoffMin) {
			return progress
		}
	}
	return progress
}

func (p *kafkaPair) verifyKafkaContinuity(b config.Kafka, position store.KafkaPosition) error {
	current := p.leaders.Request.Epoch
	if position.NextOffset < 0 || position.LeaderEpoch < -1 || current < 0 || current < position.LeaderEpoch {
		return kafkawire.ErrFrame
	}
	if position.LeaderEpoch == -1 {
		return nil // Explicit initial position has no saved epoch history.
	}
	body, err := kafkawire.EpochEnd(b.Route.Topic, b.Route.Partition, current, position.LeaderEpoch)
	if err != nil {
		return err
	}
	reply, err := p.request.command(23, 3, body, time.Now().Add(kafkaDeadline))
	if err != nil {
		return err
	}
	end, err := kafkawire.EpochEndReply(reply, b.Route.Topic, b.Route.Partition, position.LeaderEpoch)
	if err != nil {
		return err
	}
	if end.EndOffset < position.NextOffset {
		return kafkawire.ErrFrame
	}
	return nil
}
