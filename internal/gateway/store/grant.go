package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type AttemptKind string

const (
	SendAttempt   AttemptKind = "send"
	LookupAttempt AttemptKind = "lookup"
)

var ErrNotReady = errors.New("attempt_not_ready")

// Grant is an unforgeable, store-bound, single-consumption permission for one
// connection attempt. It is not serializable and cannot survive store restart.
type Grant struct{ state *grantState }
type grantState struct {
	owner                 *Store
	used                  bool
	consumedAt            time.Time
	acceptance            []byte
	cardAcceptance        []byte
	topicAcceptance       []byte
	roomAcceptance        []byte
	pinAcceptance         []byte
	activityAcceptance    []byte
	replySignalAcceptance []byte
	pollAcceptance        []byte
	observedAt            time.Time
	attempt               Attempt
}
type Attempt struct {
	ID         string
	Kind       AttemptKind
	Deadline   time.Time
	Generation int64
	Job        Job
	Backend    config.Binding
}

// Consume is called exactly once by the backend before any dial/network work.
// A committed pre-revocation grant remains usable until its original deadline.
// The backend must close its connection at that deadline, without reconnecting.
func (g *Grant) Consume(now time.Time) (Attempt, error) {
	if g == nil || g.state == nil || g.state.owner == nil {
		return Attempt{}, ErrNotReady
	}
	state := g.state
	s := state.owner
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Attempt{}, ErrClosed
	}
	if state.used || !now.Before(state.attempt.Deadline) {
		return Attempt{}, ErrNotReady
	}
	state.used = true
	state.consumedAt = now
	a := state.attempt
	if a.Job.Request.Target != nil {
		value := *a.Job.Request.Target
		a.Job.Request.Target = &value
	}
	if a.Job.Request.IssuedAt != nil {
		value := *a.Job.Request.IssuedAt
		a.Job.Request.IssuedAt = &value
	}
	if a.Job.Request.ExpiresAt != nil {
		value := *a.Job.Request.ExpiresAt
		a.Job.Request.ExpiresAt = &value
	}
	if a.Job.Request.DeliveryPolicy != nil {
		value := *a.Job.Request.DeliveryPolicy
		a.Job.Request.DeliveryPolicy = &value
	}
	if a.Job.Request.Topic != nil {
		value := *a.Job.Request.Topic
		a.Job.Request.Topic = &value
	}
	if a.Job.Request.Room != nil {
		value := *a.Job.Request.Room
		if value.SlowSeconds != nil {
			copy := *value.SlowSeconds
			value.SlowSeconds = &copy
		}
		if value.HistoryLimit != nil {
			copy := *value.HistoryLimit
			value.HistoryLimit = &copy
		}
		a.Job.Request.Room = &value
	}
	if a.Job.Request.ReplySignal != nil {
		value := *a.Job.Request.ReplySignal
		a.Job.Request.ReplySignal = &value
	}
	if a.Job.Request.Activity != nil {
		value := *a.Job.Request.Activity
		a.Job.Request.Activity = &value
	}
	if a.Job.Request.Poll != nil {
		value := *a.Job.Request.Poll
		value.Options = append([]string(nil), value.Options...)
		a.Job.Request.Poll = &value
	}
	if a.Job.Request.Profile != nil {
		value := *a.Job.Request.Profile
		if value.Model != nil {
			copy := *value.Model
			value.Model = &copy
		}
		if value.Workspace != nil {
			copy := *value.Workspace
			value.Workspace = &copy
		}
		if value.Tools != nil {
			copy := *value.Tools
			value.Tools = &copy
		}
		if value.About != nil {
			copy := *value.About
			value.About = &copy
		}
		a.Job.Request.Profile = &value
	}
	if a.Job.Request.Presence != nil {
		value := *a.Job.Request.Presence
		a.Job.Request.Presence = &value
	}
	return a, nil
}

// Reserve is used only by the trusted dispatcher. It increments the independent
// send/lookup budget and commits an exclusive attempt marker before returning a
// handle. It performs no I/O beyond the journal and does not reserve a session.
func (s *Store) Reserve(ctx context.Context, principal, requestID string, kind AttemptKind, now time.Time) (*Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-30 || kind != SendAttempt && kind != LookupAttempt {
		return nil, ErrInvalid
	}
	var binding string
	for _, p := range s.config.Principals {
		if p.ID == principal {
			binding = p.Binding
		}
	}
	if binding == "" {
		return nil, auth.ErrForbidden
	}
	if err := s.checkpoint(ctx); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, principal, binding, requestID)
	if err != nil {
		return nil, err
	}
	if err := j.Request.Normalize(); err != nil || j.Request.Operation != core.Send && j.Request.Operation != core.Reply && j.Request.Operation != core.ReactionAdd && j.Request.Operation != core.ProfileUpdate && j.Request.Operation != core.PresenceSet && j.Request.Operation != core.TopicSet && j.Request.Operation != core.RoomUpdate && j.Request.Operation != core.PinsAdd && j.Request.Operation != core.PinsRemove && j.Request.Operation != core.ActivitySet && j.Request.Operation != core.ReplySignalSet && j.Request.Operation != core.ReplySignalCancel && j.Request.Operation != core.PollCreate && j.Request.Operation != core.PollVote && j.Request.Operation != core.PollClose && j.Request.Operation != core.MessageAction && j.Request.Operation != core.MessageCorrect && j.Request.Operation != core.MessageRetract {
		return nil, ErrInvalid
	}
	for _, p := range s.config.Principals {
		if p.ID == principal && (p.Revoked || !slices.Contains(p.Operations, j.Request.Operation) || (j.Request.Target != nil && !slices.Contains(p.Targets, *j.Request.Target))) {
			return nil, auth.ErrForbidden
		}
	}
	var revoked bool
	var generation int64
	if err := tx.QueryRowContext(ctx, "SELECT revoked,generation FROM authorization WHERE principal=? AND binding=?", principal, binding).Scan(&revoked, &generation); err != nil {
		return nil, err
	}
	if revoked {
		return nil, auth.ErrForbidden
	}
	var active sql.NullString
	var sendBlocked bool
	var sendLimit, lookupLimit, lookupForSend int
	var nextSend, nextLookup int64
	if err := tx.QueryRowContext(ctx, "SELECT attempt_id,send_limit,lookup_limit,next_send_at,next_lookup_at,lookup_for_send,send_blocked FROM jobs WHERE principal=? AND binding=? AND request_id=?", principal, binding, requestID).Scan(&active, &sendLimit, &lookupLimit, &nextSend, &nextLookup, &lookupForSend, &sendBlocked); err != nil {
		return nil, err
	}
	if active.Valid || j.Result.ExpiresAt <= n || j.Result.State == core.Accepted || j.Result.State == core.Rejected && (kind == SendAttempt || !j.Result.AcceptanceUncertain) || j.Result.State == core.Expired || j.Result.State == core.Blocked {
		return nil, ErrNotReady
	}
	if kind == SendAttempt && (sendBlocked || j.Result.SendAttempts >= sendLimit || nextSend > n || j.Policy == core.ReceiptOnly && j.Result.SendAttempts > 0) {
		return nil, ErrNotReady
	}
	if kind == LookupAttempt && (j.Result.LookupAttempts >= lookupLimit || nextLookup > n || j.Result.SendAttempts == 0 || j.Policy == core.Retry && !sendBlocked && j.Result.SendAttempts < sendLimit && lookupForSend == j.Result.SendAttempts) {
		return nil, ErrNotReady
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(token[:])
	deadline := min(n+30, j.Result.ExpiresAt)
	query := `UPDATE jobs SET attempt_id=?,attempt_kind=?,attempt_deadline=?,authorization_generation=?,lookup_attempts=lookup_attempts+1,lookup_for_send=send_attempts WHERE principal=? AND binding=? AND request_id=?`
	if kind == SendAttempt {
		query = `UPDATE jobs SET attempt_id=?,attempt_kind=?,attempt_deadline=?,authorization_generation=?,send_attempts=send_attempts+1,state='dispatching',accepted=NULL,duplicate_risk=CASE WHEN may_have_accepted=1 THEN 1 ELSE duplicate_risk END WHERE principal=? AND binding=? AND request_id=?`
	}
	if _, err := tx.ExecContext(ctx, query, id, kind, deadline, generation, principal, binding, requestID); err != nil {
		return nil, err
	}
	j, err = readJob(ctx, tx, principal, binding, requestID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	var backend config.Binding
	for _, b := range s.config.Bindings {
		if b.ID == binding {
			backend = b
		}
	}
	return &Grant{state: &grantState{owner: s, attempt: Attempt{ID: id, Kind: kind, Deadline: time.Unix(deadline, 0), Generation: generation, Job: j, Backend: backend}}}, nil
}
