package store

import (
	"context"
	"time"
)

type Maintenance struct {
	Expired          int64
	Results          int64
	Replay           int64
	IRCEvents        int64
	MatrixEvents     int64
	GitHubEvents     int64
	ChirpStackEvents int64
}

// Maintain expires idle work and prunes only resolved records past their stored
// retention window. It never clears an active grant, resolves uncertainty or
// changes a job's lifetime/budget. Historical Kafka records are preserved, including
// jobs awaiting result publication. Counts describe committed changes only.
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
AND NOT EXISTS (SELECT 1 FROM kafka_records kr WHERE kr.principal=jobs.principal
AND kr.binding=jobs.binding AND kr.request_id=jobs.request_id AND kr.completed_at IS NULL)
AND (may_have_accepted=0 OR (state='accepted' AND (receipt IS NOT NULL OR card_outcome IS NOT NULL OR topic_outcome IS NOT NULL OR room_outcome IS NOT NULL OR pin_outcome IS NOT NULL OR activity_outcome IS NOT NULL OR reply_signal_outcome IS NOT NULL OR poll_outcome IS NOT NULL)))`, &counts.Results, []any{n}},
		{`DELETE FROM irc_events WHERE (expires_seconds<? OR (expires_seconds=? AND expires_nanos<=?))
AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.request_id=irc_events.request_id)`, &counts.IRCEvents, []any{n, n, now.Nanosecond()}},
		{`DELETE FROM matrix_events WHERE (expires_seconds<? OR (expires_seconds=? AND expires_nanos<=?))
AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.request_id=matrix_events.request_id)`, &counts.MatrixEvents, []any{n, n, now.Nanosecond()}},
		{`DELETE FROM github_events WHERE (expires_seconds<? OR (expires_seconds=? AND expires_nanos<=?))
AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.request_id=github_events.request_id)`, &counts.GitHubEvents, []any{n, n, now.Nanosecond()}},
		{`DELETE FROM chirpstack_events WHERE (expires_seconds<? OR (expires_seconds=? AND expires_nanos<=?))
AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.request_id=chirpstack_events.request_id)`, &counts.ChirpStackEvents, []any{n, n, now.Nanosecond()}},
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
