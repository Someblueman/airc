package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type kafkaRecordState struct {
	outcomeAt          sql.NullInt64
	route              config.Kafka
	position           KafkaPosition
	request            core.Request
	outcome            []byte
	admitted, intentAt int64
	completed          sql.NullInt64
}

// kafkaRecordState resolves the opaque association against durable ownership
// and current authority. The caller holds the store lock.
func (s *Store) kafkaRecordState(ctx context.Context, record KafkaRecord) (kafkaRecordState, error) {
	if s.closed {
		return kafkaRecordState{}, ErrClosed
	}
	if record.owner != s {
		return kafkaRecordState{}, ErrInvalid
	}
	b, binding, err := s.kafkaOwner(ctx, record.ownerID)
	if err != nil {
		return kafkaRecordState{}, err
	}
	if record.principal != b.Route.Principal || record.binding != binding {
		return kafkaRecordState{}, auth.ErrForbidden
	}
	position, err := s.kafkaPosition(ctx, b, binding)
	if err != nil {
		return kafkaRecordState{}, err
	}
	state := kafkaRecordState{route: b, position: position}
	var data []byte
	var id string
	var expected int64
	err = s.db.QueryRowContext(ctx, `SELECT kr.record_id,kr.expected_position,kr.admitted_at,kr.completed_at,ki.request,ki.outcome,ki.admitted_at,ki.outcome_at
 FROM kafka_records kr JOIN kafka_intents ki USING(principal,binding,request_id)
 WHERE kr.namespace=? AND kr.topic=? AND kr.partition=? AND kr.offset=?
 AND kr.principal=? AND kr.binding=? AND kr.request_id=?`, b.Namespace, b.Route.Topic, b.Route.Partition, record.offset, record.principal, record.binding, record.requestID).Scan(&id, &expected, &state.admitted, &state.completed, &data, &state.outcome, &state.intentAt, &state.outcomeAt)
	if errors.Is(err, sql.ErrNoRows) {
		return kafkaRecordState{}, ErrNotFound
	}
	if err != nil {
		return kafkaRecordState{}, err
	}
	if id != record.recordID || expected != record.expected.NextOffset {
		return kafkaRecordState{}, ErrConflict
	}
	if err := json.Unmarshal(data, &state.request); err != nil {
		return kafkaRecordState{}, err
	}
	if state.request.Target == nil || *state.request.Target != b.Route.Target || !slices.Contains(b.Route.Operations, state.request.Operation) {
		return kafkaRecordState{}, auth.ErrForbidden
	}
	if !state.completed.Valid && position != record.expected {
		return kafkaRecordState{}, ErrConflict
	}
	return state, nil
}

// KafkaOutcome returns an owned copy of the first retained complete semantic
// outcome. Nil means no outcome has committed; the caller may compute it.
func (s *Store) KafkaOutcome(ctx context.Context, record KafkaRecord) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.kafkaRecordState(ctx, record)
	if err != nil {
		return nil, err
	}
	return slices.Clone(state.outcome), nil
}
