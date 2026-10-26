package store

import (
	"context"
	"errors"
)

// Checkpoint reports busy or failed truncation instead of silently accumulating
// WAL writes. Intake also requires this barrier before its next transaction.
func (s *Store) Checkpoint(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return s.checkpoint(ctx)
}

func (s *Store) checkpoint(ctx context.Context) error {
	var busy, log, done int
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done); err != nil {
		return err
	}
	if busy != 0 || log != done {
		return errors.New("journal checkpoint incomplete; intake stopped")
	}
	return nil
}
