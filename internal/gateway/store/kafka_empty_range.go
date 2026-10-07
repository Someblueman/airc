package store

import (
	"context"
	"database/sql"
	"errors"
)

// AdvanceKafkaEmptyRange consumes a proven empty compacted range from the
// trusted owner. The owner must validate the whole Fetch reply, log boundaries
// and nondecreasing leader epoch, and prove there are no unprocessed records
// in [expected.NextOffset,next). A watermark alone is never this proof.
// No job or sender outcome is created, and pending records always block it.
func (s *Store) AdvanceKafkaEmptyRange(ctx context.Context, ownerID string, expected KafkaPosition, next int64, epoch int32) (KafkaPosition, error) {
	if expected.NextOffset < 0 || expected.LeaderEpoch < -1 || next <= expected.NextOffset || epoch < 0 || epoch < expected.LeaderEpoch {
		return KafkaPosition{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return KafkaPosition{}, ErrClosed
	}
	b, binding, err := s.kafkaOwner(ctx, ownerID)
	if err != nil {
		return KafkaPosition{}, err
	}
	position, err := s.kafkaPosition(ctx, b, binding)
	if errors.Is(err, sql.ErrNoRows) {
		return KafkaPosition{}, ErrNotFound
	}
	if err != nil {
		return KafkaPosition{}, err
	}
	if position != expected {
		return KafkaPosition{}, ErrConflict
	}
	if err := s.checkpoint(ctx); err != nil {
		return KafkaPosition{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KafkaPosition{}, err
	}
	defer tx.Rollback()
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM kafka_records WHERE namespace=? AND topic=? AND partition=? AND completed_at IS NULL`, b.Namespace, b.Route.Topic, b.Route.Partition).Scan(&pending); err != nil {
		return KafkaPosition{}, err
	}
	if pending != 0 {
		return KafkaPosition{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE kafka_positions SET next_offset=?,leader_epoch=? WHERE namespace=? AND topic=? AND partition=? AND next_offset=? AND leader_epoch=?`, next, epoch, b.Namespace, b.Route.Topic, b.Route.Partition, expected.NextOffset, expected.LeaderEpoch)
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
	if err := tx.Commit(); err != nil {
		return KafkaPosition{}, err
	}
	return KafkaPosition{NextOffset: next, LeaderEpoch: epoch}, nil
}
