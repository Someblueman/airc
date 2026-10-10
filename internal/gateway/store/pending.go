package store

import "context"

// HasPending reports unresolved delivery or an active grant. It is a shutdown
// scheduling hint, not permission to send and not evidence of upstream delivery.
func (s *Store) HasPending(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrClosed
	}
	var pending bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE terminal_at IS NULL OR attempt_id IS NOT NULL)`).Scan(&pending)
	return pending, err
}
