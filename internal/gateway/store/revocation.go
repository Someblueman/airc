package store

import (
	"context"
	"database/sql"
	"errors"
)

// Revoke is a trusted local-operator operation, never a caller body operation.
// The global SQLite gate orders its commit with intake and attempt reservations.
// Existing granted attempts may finish once; no new grant or intake may follow.
func (s *Store) Revoke(ctx context.Context, principal string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
	if err := s.checkpoint(ctx); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `UPDATE authorization SET generation=generation+CASE WHEN revoked=0 THEN 1 ELSE 0 END,revoked=1 WHERE principal=? RETURNING generation`, principal).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return generation, nil
}
