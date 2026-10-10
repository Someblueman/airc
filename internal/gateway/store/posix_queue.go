package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
)

// POSIXQueueOwnership is trusted ingress ownership bookkeeping. Only a caller
// holding a newly exclusively created queue may register or replace a record.
// It is neither proof of message intake nor authentication of a producer.
type POSIXQueueOwnership struct {
	Listener, Name                                           string
	Depth                                                    int
	BootID                                                   string
	NamespaceDevice, NamespaceInode, QueueDevice, QueueInode uint64
}

const maxPOSIXQueueOwners = 1024

func validPOSIXQueueOwnership(r POSIXQueueOwnership) bool {
	if len(r.Listener) < 1 || len(r.Listener) > 64 || strings.Trim(r.Listener, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		return false
	}
	if len(r.Name) < 2 || len(r.Name) > 255 || r.Name[0] != '/' || strings.Trim(r.Name[1:2], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" || strings.Trim(r.Name[1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-") != "" {
		return false
	}
	if r.Depth < 1 || r.Depth > 10 || len(r.BootID) != 36 {
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
	return r.NamespaceInode > 0 && r.QueueInode > 0 && r.NamespaceDevice <= math.MaxInt64 && r.NamespaceInode <= math.MaxInt64 && r.QueueDevice <= math.MaxInt64 && r.QueueInode <= math.MaxInt64
}

func (s *Store) LoadPOSIXQueueOwnership(ctx context.Context, listener string) (POSIXQueueOwnership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return POSIXQueueOwnership{}, ErrClosed
	}
	return s.loadPOSIXQueueOwnership(ctx, listener)
}
func (s *Store) loadPOSIXQueueOwnership(ctx context.Context, listener string) (POSIXQueueOwnership, error) {
	r := POSIXQueueOwnership{Listener: listener}
	err := s.db.QueryRowContext(ctx, `SELECT name,depth,boot_id,namespace_device,namespace_inode,queue_device,queue_inode FROM posix_queue_ownership WHERE listener=?`, listener).Scan(&r.Name, &r.Depth, &r.BootID, &r.NamespaceDevice, &r.NamespaceInode, &r.QueueDevice, &r.QueueInode)
	if errors.Is(err, sql.ErrNoRows) {
		return POSIXQueueOwnership{}, ErrNotFound
	}
	if err != nil {
		return POSIXQueueOwnership{}, err
	}
	if !validPOSIXQueueOwnership(r) {
		return POSIXQueueOwnership{}, ErrInvalid
	}
	return r, nil
}

// SavePOSIXQueueOwnership uses compare-and-replace against the complete previous
// record; nil requires absence. The store retains its exclusive process lock and
// serialized connection. Unknown existing queues must never be registered here.
func (s *Store) SavePOSIXQueueOwnership(ctx context.Context, next POSIXQueueOwnership, previous *POSIXQueueOwnership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !validPOSIXQueueOwnership(next) || previous != nil && (!validPOSIXQueueOwnership(*previous) || previous.Listener != next.Listener) {
		return ErrInvalid
	}
	current, err := s.loadPOSIXQueueOwnership(ctx, next.Listener)
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
	err = s.db.QueryRowContext(ctx, "SELECT listener FROM posix_queue_ownership WHERE name=? AND listener<>?", next.Name, next.Listener).Scan(&other)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous == nil {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM posix_queue_ownership").Scan(&count); err != nil {
			return err
		}
		if count >= maxPOSIXQueueOwners {
			return ErrCapacity
		}
	}
	if err := s.checkpoint(ctx); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO posix_queue_ownership VALUES(?,?,?,?,?,?,?,?)
 ON CONFLICT(listener) DO UPDATE SET name=excluded.name,depth=excluded.depth,boot_id=excluded.boot_id,namespace_device=excluded.namespace_device,namespace_inode=excluded.namespace_inode,queue_device=excluded.queue_device,queue_inode=excluded.queue_inode`, next.Listener, next.Name, next.Depth, next.BootID, next.NamespaceDevice, next.NamespaceInode, next.QueueDevice, next.QueueInode)
	return err
}
