package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

// RetainKafkaOutcome commits a whole semantic result before publication. The
// dispatcher validates its operation-specific shape and preflights the final
// signed token. Concurrent identical-intent computations return the first
// committed winner, never a replacement page or cursor.
func (s *Store) RetainKafkaOutcome(ctx context.Context, record KafkaRecord, outcome []byte, now time.Time) ([]byte, error) {
	if len(outcome) == 0 || len(outcome) > auth.MaxSignedBytes || now.Unix() < 0 {
		return nil, ErrInvalid
	}
	outcome = slices.Clone(outcome)
	if !json.Valid(outcome) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.kafkaRecordState(ctx, record)
	if err != nil {
		return nil, err
	}
	if state.outcome != nil {
		return slices.Clone(state.outcome), nil
	}
	if state.completed.Valid || now.Unix() < state.intentAt || now.Unix() < state.admitted {
		return nil, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE kafka_intents SET outcome=?,outcome_at=? WHERE principal=? AND binding=? AND request_id=? AND outcome IS NULL`, outcome, now.Unix(), record.principal, record.binding, record.requestID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return slices.Clone(outcome), nil
}

// CompleteKafkaPublication is called by the trusted command owner only after
// a validated successful all-replica Produce reply. It atomically marks the
// record completed and advances its exact saved prior position. Produce proves
// broker publication only; it never changes native Send acceptance.
func (s *Store) CompleteKafkaPublication(ctx context.Context, record KafkaRecord, epoch int32, now time.Time) (KafkaPosition, error) {
	if epoch < 0 || now.Unix() < 0 {
		return KafkaPosition{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.kafkaRecordState(ctx, record)
	if err != nil {
		return KafkaPosition{}, err
	}
	if state.completed.Valid {
		return state.position, nil
	}
	if state.outcome == nil || epoch < state.position.LeaderEpoch {
		return KafkaPosition{}, ErrConflict
	}
	if now.Unix() < state.admitted || !state.outcomeAt.Valid || now.Unix() < state.outcomeAt.Int64 {
		return KafkaPosition{}, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return KafkaPosition{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KafkaPosition{}, err
	}
	defer tx.Rollback()
	b := state.route
	position := KafkaPosition{NextOffset: record.offset + 1, LeaderEpoch: epoch}
	result, err := tx.ExecContext(ctx, `UPDATE kafka_positions SET next_offset=?,leader_epoch=? WHERE namespace=? AND topic=? AND partition=? AND next_offset=? AND leader_epoch=?`, position.NextOffset, epoch, b.Namespace, b.Route.Topic, b.Route.Partition, record.expected.NextOffset, record.expected.LeaderEpoch)
	if err != nil {
		return KafkaPosition{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return KafkaPosition{}, err
	}
	if affected != 1 {
		return KafkaPosition{}, ErrConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE kafka_records SET completed_at=? WHERE namespace=? AND topic=? AND partition=? AND offset=? AND completed_at IS NULL`, now.Unix(), b.Namespace, b.Route.Topic, b.Route.Partition, record.offset)
	if err != nil {
		return KafkaPosition{}, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return KafkaPosition{}, err
	}
	if affected != 1 {
		return KafkaPosition{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return KafkaPosition{}, err
	}
	return position, nil
}
