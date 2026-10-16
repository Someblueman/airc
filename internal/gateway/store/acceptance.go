package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

var ErrStaleAttempt = errors.New("stale_attempt")

// Accept preserves AIRC's acknowledgement of the grant's mutation before
// returning a durable accepted result. The evidence must be the one kind the
// operation produces, correlated with the stored intent; only an operation
// with lookups may be accepted through a lookup grant. observedAt is the
// backend's local observation time, not the server's message time. Saving
// may finish after expiry or revocation. now starts terminal retention at
// durable completion, never before observation. A failed save keeps a private
// in-memory copy on the grant for identical retries; the active marker
// remains, so no other attempt may be reserved in this process.
func (s *Store) Accept(ctx context.Context, g *Grant, evidence core.Acceptance, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	a := state.attempt
	if observedAt.Before(state.consumedAt) || !observedAt.Before(a.Deadline) || now.Before(observedAt) || a.Kind != SendAttempt && !a.Job.Request.Operation.HasLookup() {
		return Job{}, ErrInvalid
	}
	data, err := evidence.Encode(a.Job.Request, a.Job.UpstreamID, a.Job.Author.Nick)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.acceptance != nil && !bytes.Equal(state.acceptance, data) {
		return Job{}, ErrConflict
	}
	// Keep this before SQLite work. Commit failure cannot erase observed acceptance.
	state.acceptance = data
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

// The Record*Acceptance methods are Accept with one kind of evidence.

func (s *Store) RecordAcceptance(ctx context.Context, g *Grant, receipt *irc.SendReceiptEvent, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Receipt: receipt}, observedAt, now)
}

func (s *Store) RecordCardAcceptance(ctx context.Context, g *Grant, ack *core.CardAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Card: ack}, observedAt, now)
}

func (s *Store) RecordTopicAcceptance(ctx context.Context, g *Grant, ack *core.TopicAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Topic: ack}, observedAt, now)
}

func (s *Store) RecordRoomAcceptance(ctx context.Context, g *Grant, ack *core.RoomAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Room: ack}, observedAt, now)
}

func (s *Store) RecordPinAcceptance(ctx context.Context, g *Grant, ack *core.PinAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Pin: ack}, observedAt, now)
}

func (s *Store) RecordActivityAcceptance(ctx context.Context, g *Grant, ack *core.ActivityAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Activity: ack}, observedAt, now)
}

func (s *Store) RecordReplySignalAcceptance(ctx context.Context, g *Grant, ack *core.ReplySignalAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{ReplySignal: ack}, observedAt, now)
}

func (s *Store) RecordPollAcceptance(ctx context.Context, g *Grant, ack *core.PollAcknowledgement, observedAt, now time.Time) (Job, error) {
	return s.Accept(ctx, g, core.Acceptance{Poll: ack}, observedAt, now)
}

// RetryAcceptance saves the original private copy after a journal failure. It
// never sends and does not depend on a mutable caller-owned evidence object.
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

// saveAcceptance commits the grant's evidence, its account ID and the
// accepted state in one transaction. Saving the same evidence again is
// idempotent; different evidence already saved for the job is a conflict.
func (s *Store) saveAcceptance(ctx context.Context, state *grantState, now time.Time) (Job, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	a := state.attempt
	var active sql.NullString
	var stored []byte
	if err := tx.QueryRowContext(ctx, "SELECT attempt_id,outcome FROM jobs WHERE author_scope=? AND request_id=?", a.Job.Scope, a.Job.Request.RequestID).Scan(&active, &stored); err != nil {
		return Job{}, err
	}
	if stored != nil {
		if !bytes.Equal(stored, state.acceptance) {
			return Job{}, ErrConflict
		}
	} else {
		if !active.Valid || active.String != a.ID {
			return Job{}, ErrStaleAttempt
		}
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,outcome=?,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, state.acceptance, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
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
	if err := saveAccount(ctx, tx, state); err != nil {
		return Job{}, err
	}
	j, err := readJob(ctx, tx, a.Job.Scope, a.Job.Request.RequestID)
	if err != nil {
		return Job{}, err
	}
	if err := commit(ctx, tx); err != nil {
		return Job{}, err
	}
	j.Result.NextAction = "none"
	s.saved(j)
	return j, nil
}
