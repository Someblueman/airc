package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

var (
	ErrConflict = errors.New(string(core.IDConflict))
	ErrCapacity = errors.New(string(core.CapacityExceeded))
	ErrInvalid  = errors.New(string(core.InvalidRequest))
	ErrNotFound = errors.New("request_unknown")
	ErrClosed   = errors.New("journal_closed")
)

type Job struct {
	Principal   string
	Binding     string
	UpstreamID  string
	Request     core.Request
	Policy      core.Policy
	Result      core.Result
	ReplayUntil int64
}

func (s *Store) allowed(identity auth.Identity, r core.Request) error {
	if err := identity.Authorize(r); err != nil {
		return err
	}
	for _, p := range s.config.Principals {
		if p.ID == identity.ID() && p.Binding == identity.Binding() && !p.Revoked && slices.Contains(p.Operations, r.Operation) && (r.Target == nil || slices.Contains(p.Targets, *r.Target)) {
			return nil
		}
	}
	return auth.ErrForbidden
}

// Admit atomically coalesces normalized explicit intent and persists its first
// effective defaults. replayUntil is trusted codec output (zero for sessions),
// never a caller-provided body field. Only mutations enter the delivery journal.
func (s *Store) Admit(ctx context.Context, identity auth.Identity, r core.Request, replayUntil int64, now time.Time) (Job, bool, error) {
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if err := r.Normalize(); err != nil || r.Operation != core.Send && r.Operation != core.Reply {
		return Job{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, false, ErrClosed
	}
	if err := s.allowed(identity, r); err != nil {
		return Job{}, false, err
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return Job{}, false, ErrInvalid
	}
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds || replayUntil < 0 {
		return Job{}, false, ErrInvalid
	}
	if replayUntil > 0 && (r.IssuedAt == nil || r.ExpiresAt == nil || *r.ExpiresAt > math.MaxInt64-auth.ClockSkewSeconds || replayUntil != *r.ExpiresAt+auth.ClockSkewSeconds) {
		return Job{}, false, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return Job{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	var oldFingerprint string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM jobs WHERE principal=? AND binding=? AND request_id=?", identity.ID(), identity.Binding(), r.RequestID).Scan(&oldFingerprint)
	if err == nil {
		if oldFingerprint != fingerprint {
			return Job{}, false, ErrConflict
		}
		// Extend the marker if a later valid signature covers the same intent.
		if replayUntil > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO replay VALUES (?,?,?,?,?) ON CONFLICT(principal,binding,request_id) DO UPDATE SET replay_until=max(replay_until,excluded.replay_until)`, identity.ID(), identity.Binding(), r.RequestID, fingerprint, replayUntil); err != nil {
				return Job{}, false, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE jobs SET replay_until=max(replay_until,?) WHERE principal=? AND binding=? AND request_id=?", replayUntil, identity.ID(), identity.Binding(), r.RequestID); err != nil {
				return Job{}, false, err
			}
		}
		job, err := readJob(ctx, tx, identity, r.RequestID)
		if err != nil {
			return Job{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return Job{}, false, err
		}
		return job, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	// A retained replay marker must not recreate a pruned result.
	var until int64
	err = tx.QueryRowContext(ctx, "SELECT replay_until FROM replay WHERE principal=? AND binding=? AND request_id=?", identity.ID(), identity.Binding(), r.RequestID).Scan(&until)
	if err == nil && until >= n {
		return Job{}, false, ErrConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	issued := n
	if r.IssuedAt != nil {
		issued = *r.IssuedAt
	}
	if issued > math.MaxInt64-s.config.Retry.LifetimeSeconds || issued > n && issued-n > auth.ClockSkewSeconds {
		return Job{}, false, ErrInvalid
	}
	expires := issued + s.config.Retry.LifetimeSeconds
	if r.ExpiresAt != nil {
		expires = *r.ExpiresAt
	}
	if expires <= issued || expires <= n || expires-issued > s.config.Retry.LifetimeSeconds {
		return Job{}, false, ErrInvalid
	}
	policy := s.config.Retry.Policy
	if r.DeliveryPolicy != nil {
		policy = *r.DeliveryPolicy
	}
	var total, owned int
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(principal=?),0) FROM jobs WHERE terminal_at IS NULL`, identity.ID()).Scan(&total, &owned); err != nil {
		return Job{}, false, err
	}
	if total >= s.config.Limits.Pending || owned >= s.config.Limits.PendingPerPrincipal {
		return Job{}, false, ErrCapacity
	}
	upstream, err := core.UpstreamID(s.config.Realm, identity.Binding(), identity.ID(), r.RequestID)
	if err != nil {
		return Job{}, false, ErrInvalid
	}
	data, err := json.Marshal(r)
	if err != nil {
		return Job{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES (?,?,?,?,?,?,?,'queued',?,?,?,?,?,?,?,?,?,?)`, identity.ID(), identity.Binding(), r.RequestID, fingerprint, upstream, data, policy, n, expires, replayUntil, s.config.Retry.TerminalRetentionSeconds, s.config.Retry.SendAttempts, s.config.Retry.LookupAttempts, s.config.Retry.BackoffSeconds, s.config.Retry.MaxBackoffSeconds, n, n)
	if err != nil {
		return Job{}, false, err
	}
	if replayUntil > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO replay VALUES (?,?,?,?,?) ON CONFLICT(principal,binding,request_id) DO UPDATE SET fingerprint=excluded.fingerprint,replay_until=excluded.replay_until`, identity.ID(), identity.Binding(), r.RequestID, fingerprint, replayUntil); err != nil {
			return Job{}, false, err
		}
	}
	job, err := readJob(ctx, tx, identity, r.RequestID)
	if err != nil {
		return Job{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func readJob(ctx context.Context, tx *sql.Tx, identity auth.Identity, requestID string) (Job, error) {
	j := Job{Principal: identity.ID(), Binding: identity.Binding(), Result: core.Result{Version: 1, RequestID: requestID, Durable: true}}
	var data []byte
	var message, failure sql.NullString
	var accepted, persisted, connected sql.NullBool
	err := tx.QueryRowContext(ctx, `SELECT upstream_id,request,policy,state,expires_at,replay_until,send_attempts,lookup_attempts,accepted,persisted,recipient_connected,message_id,duplicate_risk,last_error FROM jobs WHERE principal=? AND binding=? AND request_id=?`, identity.ID(), identity.Binding(), requestID).Scan(&j.UpstreamID, &data, &j.Policy, &j.Result.State, &j.Result.ExpiresAt, &j.ReplayUntil, &j.Result.SendAttempts, &j.Result.LookupAttempts, &accepted, &persisted, &connected, &message, &j.Result.DuplicateRisk, &failure)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	if err = json.Unmarshal(data, &j.Request); err != nil {
		return Job{}, err
	}
	if accepted.Valid {
		j.Result.Accepted = &accepted.Bool
	}
	if persisted.Valid {
		j.Result.Persisted = &persisted.Bool
	}
	if connected.Valid {
		j.Result.RecipientConnected = &connected.Bool
	}
	j.Result.MessageID = message.String
	if failure.Valid {
		j.Result.Failure = &core.Failure{Code: core.ErrorCode(failure.String)}
	}
	return j, nil
}

// Lookup is local and scoped; it does not reserve a probe, extend expiry or send.
func (s *Store) Lookup(ctx context.Context, identity auth.Identity, requestID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	r := core.Request{Version: 1, RequestID: "status", Operation: core.ReceiptGet, LookupID: requestID}
	if err := s.allowed(identity, r); err != nil {
		return Job{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, identity, requestID)
	if err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}
