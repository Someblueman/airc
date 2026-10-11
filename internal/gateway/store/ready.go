package store

import (
	"context"
	"database/sql"
	"time"
)

// Dispatch is a scheduling hint. Reserve remains the authorization and atomic
// ownership gate. At most one due request is returned per lane: an author
// scope.
type Dispatch struct {
	Lane      string
	Scope     string
	RequestID string
	Kind      AttemptKind
}

// Ready rotates through lanes after the last selected lane. The result and
// query work are bounded by the pending quotas. Jobs waiting for a credential
// are never offered.
func (s *Store) Ready(ctx context.Context, after string, limit int, now time.Time) ([]Dispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if limit < 1 || limit > s.config.Limits.Workers || now.Unix() < 0 {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lookup takes precedence only when acceptance is uncertain and this
	// scheduled resend has not already consumed its one recovery probe.
	rows, err := tx.QueryContext(ctx, `WITH candidates AS (
SELECT author_scope AS lane,author_scope,request_id,admitted_at,
CASE WHEN may_have_accepted=1 AND send_attempts>0 AND lookup_attempts<lookup_limit
 AND (policy='receipt_only' OR send_attempts>=send_limit OR state='rejected' OR lookup_for_send<send_attempts)
 THEN 'lookup' WHEN state!='rejected' AND send_blocked=0 AND send_attempts<send_limit
 AND (policy='retry' OR send_attempts=0) THEN 'send' ELSE '' END AS kind,
next_send_at,next_lookup_at
FROM jobs
WHERE attempt_id IS NULL AND expires_at>?
AND state NOT IN ('accepted','expired','blocked','credential_wait')),
due AS (SELECT lane,author_scope,request_id,kind,
row_number() OVER (PARTITION BY lane ORDER BY admitted_at,request_id) AS position
FROM candidates WHERE (kind='lookup' AND next_lookup_at<=?) OR (kind='send' AND next_send_at<=?))
SELECT lane,author_scope,request_id,kind FROM due WHERE position=1
ORDER BY lane<=?,lane LIMIT ?`, now.Unix(), now.Unix(), now.Unix(), after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Dispatch, 0, limit)
	for rows.Next() {
		var d Dispatch
		if err := rows.Scan(&d.Lane, &d.Scope, &d.RequestID, &d.Kind); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
