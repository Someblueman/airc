package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
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
	owner      *Store
	used       bool
	consumedAt time.Time
	// acceptance is the encoded evidence the attempt observed, kept until
	// it is saved.
	acceptance []byte
	observedAt time.Time
	attempt    Attempt
	// accountID is the AIRC account a registered author's attempt logged in
	// as, saved with the attempt's outcome.
	accountID string
}
type Attempt struct {
	// Realm is the journal's realm. The upstream request ID no longer
	// encodes it, so a backend of another realm refuses the grant by this.
	Realm    string
	ID       string
	Kind     AttemptKind
	Deadline time.Time
	Job      Job
	Backend  config.Upstream
	// Credential is a registered author's in-memory account token; it is
	// empty for guests.
	Credential core.Credential
	// Source is the client source AIRC attributes the session to: the
	// submitting client's address or label while this process remembers it,
	// else "listener:<id>" of the job's listener.
	Source string
}

// Consume is called exactly once by the backend before any dial/network work.
// A committed grant remains usable until its original deadline.
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

// NoteAccount attaches the AIRC account ID a registered author's consumed
// attempt logged in as. The save of the attempt's outcome (an acceptance or a
// completion) writes it in the same transaction, for diagnosis only: it never
// changes the job's state or result. A guest's attempt records nothing.
func (g *Grant) NoteAccount(accountID string) error {
	if g == nil || g.state == nil || g.state.owner == nil {
		return ErrNotReady
	}
	if !protocol.ValidMessageID(accountID) {
		return ErrInvalid
	}
	s := g.state.owner
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if !g.state.used {
		return ErrNotReady
	}
	g.state.accountID = accountID
	return nil
}

// saveAccount writes a noted account ID inside the outcome's transaction.
func saveAccount(ctx context.Context, tx *sql.Tx, state *grantState) error {
	if state.accountID == "" {
		return nil
	}
	a := state.attempt
	_, err := tx.ExecContext(ctx, "UPDATE jobs SET account_id=? WHERE author_scope=? AND request_id=? AND registered=1", state.accountID, a.Job.Scope, a.Job.Request.RequestID)
	return err
}

// Reserve is used only by the trusted dispatcher. It increments the independent
// send/lookup budget and commits an exclusive attempt marker before returning a
// handle. It performs no I/O beyond the journal and does not reserve a session.
func (s *Store) Reserve(ctx context.Context, scope, requestID string, kind AttemptKind, now time.Time) (*Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-30 || kind != SendAttempt && kind != LookupAttempt {
		return nil, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, scope, requestID)
	if err != nil {
		return nil, err
	}
	if err := j.Request.Normalize(); err != nil || !j.Request.Operation.IsMutation() {
		return nil, ErrInvalid
	}
	backend, ok := s.config.AuthorUpstream()
	if !ok {
		return nil, auth.ErrForbidden
	}
	var credential core.Credential
	if j.Author.Registered {
		var held bool
		if credential, held = s.secrets[secretKey(scope, requestID)]; !held || !credential.MatchesCheck(j.Author.Nick, j.tokenCheck) {
			return nil, ErrNotReady
		}
	}
	source := s.sources[secretKey(scope, requestID)]
	if source == "" {
		source = ListenerSource(j.Listener)
	}
	var active sql.NullString
	var sendBlocked bool
	var sendLimit, lookupLimit, lookupForSend int
	var nextSend, nextLookup int64
	if err := tx.QueryRowContext(ctx, "SELECT attempt_id,send_limit,lookup_limit,next_send_at,next_lookup_at,lookup_for_send,send_blocked FROM jobs WHERE author_scope=? AND request_id=?", scope, requestID).Scan(&active, &sendLimit, &lookupLimit, &nextSend, &nextLookup, &lookupForSend, &sendBlocked); err != nil {
		return nil, err
	}
	if active.Valid || j.Result.ExpiresAt <= n || j.Result.State == core.Accepted || j.Result.State == core.Rejected && (kind == SendAttempt || !j.Result.AcceptanceUncertain) || j.Result.State == core.Expired || j.Result.State == core.Blocked || j.Result.State == core.CredentialWait {
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
	query := `UPDATE jobs SET attempt_id=?,attempt_kind=?,attempt_deadline=?,lookup_attempts=lookup_attempts+1,lookup_for_send=send_attempts WHERE author_scope=? AND request_id=?`
	if kind == SendAttempt {
		query = `UPDATE jobs SET attempt_id=?,attempt_kind=?,attempt_deadline=?,send_attempts=send_attempts+1,state='dispatching',accepted=NULL,duplicate_risk=CASE WHEN may_have_accepted=1 THEN 1 ELSE duplicate_risk END WHERE author_scope=? AND request_id=?`
	}
	if _, err := tx.ExecContext(ctx, query, id, kind, deadline, scope, requestID); err != nil {
		return nil, err
	}
	j, err = readJob(ctx, tx, scope, requestID)
	if err != nil {
		return nil, err
	}
	if err := commit(ctx, tx); err != nil {
		return nil, err
	}
	return &Grant{state: &grantState{owner: s, attempt: Attempt{Realm: s.config.Realm, ID: id, Kind: kind, Deadline: time.Unix(deadline, 0), Job: j, Backend: backend, Credential: credential, Source: source}}}, nil
}

// ListenerSource labels clients of a carrier without addresses for AIRC's
// per-source limits: "listener:<id>", or "gateway" without a listener.
func ListenerSource(listener string) string {
	if listener == "" {
		return "gateway"
	}
	return "listener:" + listener
}
