package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// LookupRetainedSend matches original explicit intent without admission, marker
// extension, defaults resolution or delivery work. Current Send authority is
// required even if the original signed envelope has expired.
func (s *Store) LookupRetainedSend(ctx context.Context, proof auth.RetainedSend) (Job, error) {
	r := proof.Request
	if r.Operation != core.Send {
		return Job{}, ErrInvalid
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return Job{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if err := s.allowed(ctx, proof.Identity, r); err != nil {
		return Job{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var stored string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM jobs WHERE principal=? AND binding=? AND request_id=?", proof.Identity.ID(), proof.Identity.Binding(), r.RequestID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	if stored != fingerprint {
		return Job{}, ErrConflict
	}
	j, err := readJob(ctx, tx, proof.Identity.ID(), proof.Identity.Binding(), r.RequestID)
	if err != nil {
		return Job{}, err
	}
	if j.Request.Operation != core.Send || !j.Result.Durable {
		return Job{}, ErrInvalid
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}
