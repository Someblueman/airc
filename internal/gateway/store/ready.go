package store

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// Dispatch is a scheduling hint. Reserve remains the authorization and atomic
// ownership gate. At most one due request is returned per principal.
type Dispatch struct {
	Principal string
	RequestID string
	Kind      AttemptKind
}

// Ready rotates through principal IDs after the last selected principal. The
// result and query work are bounded by configured principals/pending quotas.
func (s *Store) Ready(ctx context.Context, after string, limit int, now time.Time) ([]Dispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if limit < 1 || limit > s.config.Limits.Workers || now.Unix() < 0 {
		return nil, ErrInvalid
	}
	principals := make([]string, 0, len(s.config.Principals))
	for _, p := range s.config.Principals {
		if !p.Revoked {
			principals = append(principals, p.ID)
		}
	}
	sort.Strings(principals)
	start := sort.Search(len(principals), func(i int) bool { return principals[i] > after })
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result := make([]Dispatch, 0, min(limit, len(principals)))
	for offset := 0; offset < len(principals) && len(result) < limit; offset++ {
		principal := principals[(start+offset)%len(principals)]
		d := Dispatch{Principal: principal}
		// Lookup takes precedence only when acceptance is uncertain and this
		// scheduled resend has not already consumed its one recovery probe.
		err := tx.QueryRowContext(ctx, `WITH candidates AS (
SELECT request_id,admitted_at,
CASE WHEN may_have_accepted=1 AND send_attempts>0 AND lookup_attempts<lookup_limit
 AND (policy='receipt_only' OR send_attempts>=send_limit OR state='rejected' OR lookup_for_send<send_attempts)
 THEN 'lookup' WHEN state!='rejected' AND send_blocked=0 AND send_attempts<send_limit
 AND (policy='retry' OR send_attempts=0) THEN 'send' ELSE '' END AS kind,
next_send_at,next_lookup_at
FROM jobs JOIN authorization USING(principal,binding)
WHERE principal=? AND revoked=0 AND attempt_id IS NULL AND expires_at>?
AND state NOT IN ('accepted','expired','blocked'))
SELECT request_id,kind FROM candidates
WHERE (kind='lookup' AND next_lookup_at<=?) OR (kind='send' AND next_send_at<=?)
ORDER BY admitted_at,request_id LIMIT 1`, principal, now.Unix(), now.Unix(), now.Unix()).Scan(&d.RequestID, &d.Kind)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
