package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
)

// SystemVQueueOwnership is trusted lifecycle bookkeeping, never sender authority.
// Only an exclusively created ingress may be registered or replace a record.
// The caller retains both that queue and the store's exclusive journal lock.
type SystemVQueueOwnership struct {
	Listener, Platform              string
	Key                             int32
	ID                              int64
	Sequence                        uint16
	Changed                         int64
	BootID                          string
	NamespaceDevice, NamespaceInode uint64
}

const maxSystemVQueueOwners = 1024

func validSystemVQueueOwnership(r SystemVQueueOwnership) bool {
	if len(r.Listener) < 1 || len(r.Listener) > 64 || strings.Trim(r.Listener, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return false
	}
	if r.Key <= 0 || r.ID < 0 || r.ID > math.MaxInt32 || r.Changed <= 0 || len(r.BootID) != 36 || r.BootID == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range r.BootID {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	if r.NamespaceDevice > math.MaxInt64 || r.NamespaceInode > math.MaxInt64 {
		return false
	}
	switch r.Platform {
	case "darwin-arm64":
		return r.NamespaceDevice == 0 && r.NamespaceInode == 0
	case "linux-arm64", "linux-amd64":
		return r.NamespaceInode > 0
	default:
		return false
	}
}
func (s *Store) LoadSystemVQueueOwnership(ctx context.Context, listener string) (SystemVQueueOwnership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return SystemVQueueOwnership{}, ErrClosed
	}
	return s.loadSystemVQueueOwnership(ctx, listener)
}
func (s *Store) loadSystemVQueueOwnership(ctx context.Context, listener string) (SystemVQueueOwnership, error) {
	r := SystemVQueueOwnership{Listener: listener}
	err := s.db.QueryRowContext(ctx, `SELECT platform,queue_key,queue_id,sequence,changed,boot_id,namespace_device,namespace_inode FROM system_v_queue_ownership WHERE listener=?`, listener).Scan(&r.Platform, &r.Key, &r.ID, &r.Sequence, &r.Changed, &r.BootID, &r.NamespaceDevice, &r.NamespaceInode)
	if errors.Is(err, sql.ErrNoRows) {
		return SystemVQueueOwnership{}, ErrNotFound
	}
	if err != nil {
		return SystemVQueueOwnership{}, err
	}
	if !validSystemVQueueOwnership(r) {
		return SystemVQueueOwnership{}, ErrInvalid
	}
	return r, nil
}

// SaveSystemVQueueOwnership compares the entire previous record; nil requires
// absence. Unknown queues must never be registered, even with a matching key.
func (s *Store) SaveSystemVQueueOwnership(ctx context.Context, next SystemVQueueOwnership, previous *SystemVQueueOwnership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !validSystemVQueueOwnership(next) || previous != nil && (!validSystemVQueueOwnership(*previous) || previous.Listener != next.Listener) {
		return ErrInvalid
	}
	current, err := s.loadSystemVQueueOwnership(ctx, next.Listener)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if previous == nil {
		if err == nil {
			return ErrConflict
		}
	} else if err != nil || current != *previous {
		return ErrConflict
	}
	var other string
	err = s.db.QueryRowContext(ctx, "SELECT listener FROM system_v_queue_ownership WHERE queue_key=? AND listener<>?", next.Key, next.Listener).Scan(&other)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous == nil {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM system_v_queue_ownership").Scan(&count); err != nil {
			return err
		}
		if count >= maxSystemVQueueOwners {
			return ErrCapacity
		}
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO system_v_queue_ownership VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(listener) DO UPDATE SET platform=excluded.platform,queue_key=excluded.queue_key,queue_id=excluded.queue_id,sequence=excluded.sequence,changed=excluded.changed,boot_id=excluded.boot_id,namespace_device=excluded.namespace_device,namespace_inode=excluded.namespace_inode`, next.Listener, next.Platform, next.Key, next.ID, next.Sequence, next.Changed, next.BootID, next.NamespaceDevice, next.NamespaceInode)
	return err
}
