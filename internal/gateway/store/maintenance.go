package store

import (
	"context"
	"time"
)

type Maintenance struct {
	Expired int64
	Results int64
	Replay  int64
}

// Maintain expires idle work and prunes only resolved records past their stored
// retention window. It never clears an active grant, resolves uncertainty or
// changes a job's lifetime/budget. Counts describe committed changes only.
func (s *Store) Maintain(ctx context.Context, now time.Time) (Maintenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Maintenance{}, ErrClosed
	}
	n := now.Unix()
	if n < 0 {
		return Maintenance{}, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return Maintenance{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Maintenance{}, err
	}
	defer tx.Rollback()
	var counts Maintenance
	for _, statement := range []struct {
		query string
		count *int64
		args  []any
	}{
		{`UPDATE jobs SET state='expired',
accepted=CASE WHEN may_have_accepted=1 THEN NULL ELSE 0 END,
terminal_at=CASE WHEN may_have_accepted=1 THEN NULL ELSE ? END
WHERE expires_at<=? AND attempt_id IS NULL AND terminal_at IS NULL
AND state NOT IN ('accepted','expired')`, &counts.Expired, []any{n, n}},
		{`DELETE FROM jobs WHERE attempt_id IS NULL AND terminal_at IS NOT NULL
AND terminal_at<=?-retention_seconds
AND (may_have_accepted=0 OR (state='accepted' AND (receipt IS NOT NULL OR card_outcome IS NOT NULL OR topic_outcome IS NOT NULL)))`, &counts.Results, []any{n}},
		{`DELETE FROM replay WHERE replay_until<?`, &counts.Replay, []any{n}},
	} {
		result, err := tx.ExecContext(ctx, statement.query, statement.args...)
		if err != nil {
			return Maintenance{}, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return Maintenance{}, err
		}
		*statement.count = count
	}
	if err := tx.Commit(); err != nil {
		return Maintenance{}, err
	}
	return counts, nil
}
