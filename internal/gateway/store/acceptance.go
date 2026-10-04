package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

var ErrStaleAttempt = errors.New("stale_attempt")

const MaxReceiptBytes = 64 << 10

func validMessageID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (s *Store) receiptBytes(g *grantState, r *irc.SendReceiptEvent) ([]byte, error) {
	j := g.attempt.Job
	if r == nil || r.Type != "send_receipt" || r.Receipt == nil || !r.Receipt.Accepted || !validMessageID(r.ID) || r.RequestID != j.UpstreamID || r.Message != j.Request.Message || r.Reaction != "" || r.Timestamp.IsZero() {
		return nil, ErrInvalid
	}
	if len(r.AccountID) > 128 || len(r.Kind) > 64 || len(r.Supersedes) > 64 || len(r.SupersededBy) > 64 || len(r.PollOptions) != 0 || !r.PollClosesAt.IsZero() {
		return nil, ErrInvalid
	}
	var author string
	for _, b := range s.config.Bindings {
		if b.ID == j.Binding {
			author = b.Account
		}
	}
	from := core.Target{Kind: "direct", Name: r.From}
	if err := from.Normalize(); err != nil || author == "" || from.Name != strings.ToLower(author) {
		return nil, ErrInvalid
	}
	target := core.Target{Kind: j.Request.Target.Kind, Name: r.Target}
	if err := target.Normalize(); err != nil || target != *j.Request.Target {
		return nil, ErrInvalid
	}
	if j.Request.Operation == core.Reply {
		if r.ReplyTo != j.Request.ParentID || !validMessageID(r.ThreadID) {
			return nil, ErrInvalid
		}
	} else if r.ReplyTo != "" || r.ThreadID != "" && r.ThreadID != r.ID {
		return nil, ErrInvalid
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxReceiptBytes {
		return nil, ErrInvalid
	}
	return data, nil
}

// RecordAcceptance preserves a full correlated receipt before returning a
// durable accepted result. observedAt is the backend's local observation time,
// not the server's message time. Saving may finish after expiry or revocation.
// now starts terminal retention at durable completion, never before observation.
// A failed save keeps a private in-memory copy on the grant for identical retries;
// the active marker remains, so no other attempt may be reserved in this process.
func (s *Store) RecordAcceptance(ctx context.Context, g *Grant, receipt *irc.SendReceiptEvent, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) {
		return Job{}, ErrInvalid
	}
	data, err := s.receiptBytes(state, receipt)
	if err != nil {
		return Job{}, err
	}
	if state.acceptance != nil && !bytes.Equal(state.acceptance, data) {
		return Job{}, ErrConflict
	}
	// Keep this before SQLite work. Commit failure cannot erase observed acceptance.
	state.acceptance = data
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

// RetryAcceptance saves the original private copy after a journal failure. It
// never sends and does not depend on a mutable caller-owned receipt object.
func (s *Store) RetryAcceptance(ctx context.Context, g *Grant, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || g.state.acceptance == nil {
		return Job{}, ErrNotReady
	}
	if now.Before(g.state.observedAt) {
		return Job{}, ErrInvalid
	}
	return s.saveAcceptance(ctx, g.state, now)
}

func (s *Store) saveAcceptance(ctx context.Context, state *grantState, now time.Time) (Job, error) {
	data := state.acceptance
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	a := state.attempt
	var active sql.NullString
	var stored []byte
	if err := tx.QueryRowContext(ctx, "SELECT attempt_id,receipt FROM jobs WHERE principal=? AND binding=? AND request_id=?", a.Job.Principal, a.Job.Binding, a.Job.Request.RequestID).Scan(&active, &stored); err != nil {
		return Job{}, err
	}
	if stored != nil {
		if !bytes.Equal(stored, data) {
			return Job{}, ErrConflict
		}
	} else {
		if !active.Valid || active.String != a.ID {
			return Job{}, ErrStaleAttempt
		}
		var saved irc.SendReceiptEvent
		if err := json.Unmarshal(data, &saved); err != nil {
			return Job{}, err
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,persisted=?,recipient_connected=?,message_id=?,receipt=?,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE principal=? AND binding=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, saved.Receipt.Persisted, saved.Receipt.RecipientConnected, saved.ID, data, now.Unix(), a.Job.Principal, a.Job.Binding, a.Job.Request.RequestID, a.ID, a.Kind)
		if err != nil {
			return Job{}, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return Job{}, err
		}
		if count != 1 {
			return Job{}, ErrStaleAttempt
		}
	}
	j, err := readJob(ctx, tx, a.Job.Principal, a.Job.Binding, a.Job.Request.RequestID)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	j.Result.NextAction = "none"
	return j, nil
}
