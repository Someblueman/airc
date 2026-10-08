package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// AdmitMatrix atomically binds a homeserver network/event identity to exact content and
// native authority before creating a Send. A replay never renews its freshness.
func (s *Store) AdmitMatrix(ctx context.Context, proof auth.VerifiedMatrixMessage, now time.Time) (Job, bool, error) {
	if !proof.Valid() || now.Before(proof.Occurred()) || !now.Before(proof.ReplayDeadline()) {
		return Job{}, false, auth.ErrAuthentication
	}
	if !slices.Contains(proof.Operations(), core.Send) {
		return Job{}, false, auth.ErrForbidden
	}
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds {
		return Job{}, false, ErrInvalid
	}
	target := proof.Target()
	r := core.Request{Version: 1, RequestID: proof.RequestID(), Operation: core.Send, Target: &target, Message: proof.Body()}
	if r.Normalize() != nil || r.Message != proof.Body() || *r.Target != proof.Target() {
		return Job{}, false, ErrInvalid
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return Job{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, false, ErrClosed
	}
	if err := s.allowed(ctx, proof.Identity(), r); err != nil {
		return Job{}, false, err
	}
	if err := s.checkpoint(ctx); err != nil {
		return Job{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	var oldFingerprint, principal, binding string
	var seconds int64
	var nanos int
	err = tx.QueryRowContext(ctx, "SELECT fingerprint,principal,binding,expires_seconds,expires_nanos FROM matrix_events WHERE request_id=?", r.RequestID).Scan(&oldFingerprint, &principal, &binding, &seconds, &nanos)
	seen := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	if seen {
		if oldFingerprint != proof.Fingerprint() || principal != proof.Identity().ID() || binding != proof.Identity().Binding() || !time.Unix(seconds, int64(nanos)).Equal(proof.ReplayDeadline()) {
			return Job{}, false, ErrConflict
		}
		// An identical event cannot recreate a manually or normally pruned result.
		if _, err := readJob(ctx, tx, principal, binding, r.RequestID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Job{}, false, ErrConflict
			}
			return Job{}, false, err
		}
	} else {
		// Expired marks are removable only after their native jobs are gone. This
		// keeps event binding global across account/grant changes while work exists.
		if _, err := tx.ExecContext(ctx, `DELETE FROM matrix_events WHERE (expires_seconds<? OR (expires_seconds=? AND expires_nanos<=?)) AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.request_id=matrix_events.request_id)`, n, n, now.Nanosecond()); err != nil {
			return Job{}, false, err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM matrix_events").Scan(&count); err != nil {
			return Job{}, false, err
		}
		if count >= 16384 {
			return Job{}, false, ErrCapacity
		}
	}
	job, created, err := s.admitInTx(ctx, tx, proof.Identity(), r, 0, n, fingerprint)
	if err != nil {
		return Job{}, false, err
	}
	if !seen {
		deadline := proof.ReplayDeadline()
		if _, err := tx.ExecContext(ctx, "INSERT INTO matrix_events VALUES(?,?,?,?,?,?)", r.RequestID, proof.Fingerprint(), proof.Identity().ID(), proof.Identity().Binding(), deadline.Unix(), deadline.Nanosecond()); err != nil {
			return Job{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	return job, created, nil
}
