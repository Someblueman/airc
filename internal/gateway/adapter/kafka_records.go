package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/kafkawire"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// processKafkaRange accepts a whole codec-validated Fetch from this pair's
// request partition. An unaccepted record stops the range: only the completed
// prefix is returned, and trailing compacted coverage cannot skip the refusal.
func (p *kafkaPair) processKafkaRange(ctx context.Context, realm string, b config.Kafka, d *dispatch.Dispatcher, journal *store.Store, sealer *auth.ResponseSealer, requests chan struct{}, position store.KafkaPosition, fetched kafkawire.FetchResult) (store.KafkaPosition, error) {
	if p == nil || p.request == nil || p.result == nil || d == nil || journal == nil || sealer == nil || realm == "" || cap(requests) < 1 {
		return position, kafkawire.ErrFrame
	}
	plan, err := kafkaFetchRange(position, p.leaders.Request.Epoch, fetched)
	if err != nil {
		return position, err
	}
	for _, record := range plan.records {
		next, err := p.completeKafkaRecord(ctx, realm, b, d, journal, sealer, requests, position, record)
		if err != nil {
			return position, err
		}
		position = next
	}
	if plan.end > position.NextOffset {
		advanceCtx, cancel := context.WithTimeout(ctx, kafkaDeadline)
		defer cancel()
		next, err := journal.AdvanceKafkaEmptyRange(advanceCtx, b.ID, position, plan.end, p.leaders.Request.Epoch)
		if err != nil {
			return position, err
		}
		return next, nil
	}
	return position, nil
}

func (p *kafkaPair) completeKafkaRecord(parent context.Context, realm string, b config.Kafka, d *dispatch.Dispatcher, journal *store.Store, sealer *auth.ResponseSealer, requests chan struct{}, position store.KafkaPosition, record kafkawire.Record) (store.KafkaPosition, error) {
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
	default:
		return position, store.ErrCapacity
	}
	ctx, cancel := context.WithTimeout(parent, kafkaDeadline)
	defer cancel()
	admission, err := reserveKafkaRecord(ctx, b, d, record, position, time.Now())
	if err != nil {
		return position, err
	}
	if admission.operation != core.Send {
		readCtx, stopRead := context.WithDeadline(ctx, admission.verified.ReplayDeadline())
		defer stopRead()
		ctx = readCtx
	}
	ticket := admission.reservation.Record
	cached, err := journal.KafkaOutcome(ctx, ticket)
	if err != nil {
		return position, err
	}
	scope := kafkaResponseScope(realm, b.Route.Principal, admission.binding, b, record.Offset, ticket.RequestID())
	if cached == nil {
		result := dispatch.DetachedResult{Version: 1, Operation: admission.operation}
		if admission.operation == core.Send {
			if admission.reservation.Job == nil {
				return position, store.ErrInvalid
			}
			result.Result = &admission.reservation.Job.Result
		} else {
			result, err = d.DetachedVerified(ctx, admission.verified, time.Now())
			if err != nil {
				return position, p.publishKafkaFailure(ctx, b, journal, sealer, scope, admission, record.Offset, err)
			}
		}
		outcome := kafkaOutcome{Version: 1, RequestID: ticket.RequestID(), Operation: admission.operation, Result: &result}
		candidate, e := encodeKafkaOutcome(outcome)
		if e == nil {
			_, e = kafkaResultMessage(sealer, scope, b, record.Offset, outcome, time.Now())
		}
		if errors.Is(e, auth.ErrResponseCapacity) {
			return position, p.publishKafkaFailure(ctx, b, journal, sealer, scope, admission, record.Offset, e)
		}
		if e != nil {
			return position, e
		}
		cached, err = journal.RetainKafkaOutcome(ctx, ticket, candidate, time.Now())
		if err != nil {
			return position, err
		}
	}
	// Always use the committed first winner, including a concurrent computation.
	outcome, err := decodeKafkaOutcome(cached, ticket.RequestID(), admission.operation)
	if err != nil {
		return position, err
	}
	token, err := kafkaResultMessage(sealer, scope, b, record.Offset, outcome, time.Now())
	if errors.Is(err, auth.ErrResponseCapacity) {
		return position, p.publishKafkaFailure(ctx, b, journal, sealer, scope, admission, record.Offset, err)
	}
	if err != nil {
		return position, err
	}
	if err := p.publishKafka(ctx, b, ticket.RequestID(), token); err != nil {
		return position, err
	}
	next, err := journal.CompleteKafkaPublication(ctx, ticket, p.leaders.Request.Epoch, time.Now())
	if err != nil {
		return position, err
	}
	return next, nil
}

// A failure response never replaces a whole cached outcome or completes its
// record. Recheck durable authority even when read/preflight failed.
func (p *kafkaPair) publishKafkaFailure(ctx context.Context, b config.Kafka, journal *store.Store, sealer *auth.ResponseSealer, scope auth.ResponseScope, a kafkaRecordAdmission, offset int64, cause error) error {
	if _, err := journal.KafkaOutcome(ctx, a.reservation.Record); err != nil {
		return err
	}
	status, code := httpError(cause)
	if errors.Is(cause, auth.ErrResponseCapacity) {
		status, code = 429, core.CapacityExceeded
	}
	outcome := kafkaOutcome{Version: 1, RequestID: a.reservation.Record.RequestID(), Operation: a.operation, Error: &core.Failure{Code: code, Transient: status == 429 || status == 503}}
	token, err := kafkaResultMessage(sealer, scope, b, offset, outcome, time.Now())
	if err != nil {
		return err
	}
	if err := p.publishKafka(ctx, b, outcome.RequestID, token); err != nil {
		return err
	}
	return cause
}

func (p *kafkaPair) publishKafka(ctx context.Context, b config.Kafka, requestID string, token []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := kafkawire.Produce(b.Route.ResultTopic, b.Route.ResultPartition, []byte(requestID), token, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	reply, err := p.result.command(0, 7, body, deadline)
	if err == nil {
		_, err = kafkawire.ProduceReply(reply, b.Route.ResultTopic, b.Route.ResultPartition)
	}
	if err != nil {
		p.close()
	}
	return err
}
