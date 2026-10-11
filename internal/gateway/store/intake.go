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
	"github.com/Someblueman/airc/pkg/irc"
)

var (
	ErrConflict = errors.New(string(core.IDConflict))
	ErrCapacity = errors.New(string(core.CapacityExceeded))
	ErrInvalid  = errors.New(string(core.InvalidRequest))
	ErrNotFound = errors.New("request_unknown")
	ErrClosed   = errors.New("journal_closed")
)

// Job is one durable delivery responsibility, owned by its author scope.
// Principal and Binding are set only for jobs admitted through the temporary
// principal shim, whose credential comes from the binding's token file.
type Job struct {
	Author      core.Author
	Scope       string
	Listener    string
	AccountID   string
	Principal   string
	Binding     string
	UpstreamID  string
	Request     core.Request
	Policy      core.Policy
	Result      core.Result
	ReplayUntil int64
	Receipt     *irc.SendReceiptEvent
	tokenCheck  string
}

// Lane is the fairness and session-limit owner: the principal for shim jobs
// (their behaviour is unchanged), otherwise the author scope.
func (j Job) Lane() string { return laneOf(j.Principal, j.Scope) }

func laneOf(principal, scope string) string {
	if principal != "" {
		return "p:" + principal
	}
	return scope
}

// laneSQL mirrors laneOf for queries over jobs.
const laneSQL = `CASE WHEN principal<>'' THEN 'p:'||principal ELSE author_scope END`

// Submission is an author-level admission or lookup: who posts, the in-memory
// AIRC account token for a registered author (never stored), and the listener
// that received it. Listener is informational: identity, deduplication and
// cursors never depend on it, so a client may fall back between transports.
type Submission struct {
	Author     core.Author
	Credential core.Credential
	Listener   string
}

// submitter is the resolved owner of an admission or lookup.
type submitter struct {
	author             core.Author
	scope              string
	listener           string
	credential         core.Credential
	principal, binding string
}

func (sub submitter) tokenCheck() string {
	if sub.principal != "" || !sub.author.Registered {
		return ""
	}
	return sub.credential.Check(sub.author.Nick)
}

func (s *Store) authorSubmitter(sub Submission) (submitter, error) {
	if !sub.Author.Valid() || len(sub.Listener) > 64 || sub.Author.Registered == sub.Credential.Empty() {
		return submitter{}, ErrInvalid
	}
	if _, ok := s.config.Upstream(); !ok {
		return submitter{}, auth.ErrForbidden
	}
	return submitter{author: sub.Author, scope: sub.Author.Scope(), listener: sub.Listener, credential: sub.Credential}, nil
}

func (s *Store) principalSubmitter(identity auth.Identity) (submitter, error) {
	author, binding, ok := s.config.PrincipalAuthor(identity.ID())
	if !ok || binding.ID != identity.Binding() {
		return submitter{}, auth.ErrForbidden
	}
	return submitter{author: author, scope: author.Scope(), principal: identity.ID(), binding: identity.Binding()}, nil
}

// principalScope maps a shim principal to its author scope, or "".
func (s *Store) principalScope(principal, binding string) string {
	author, b, ok := s.config.PrincipalAuthor(principal)
	if !ok || b.ID != binding {
		return ""
	}
	return author.Scope()
}

// owns decides whether sub may see or resubmit an existing job. A shim job
// belongs to its principal alone; a registered author's job requires a
// credential matching the stored check; a guest job is anyone using that
// nickname, exactly as AIRC treats guests.
func (sub submitter) owns(j Job) error {
	if j.Principal != "" || sub.principal != "" {
		if j.Principal != sub.principal || j.Binding != sub.binding {
			return ErrNotFound
		}
		return nil
	}
	if j.Author.Registered && !sub.credential.MatchesCheck(j.Author.Nick, j.tokenCheck) {
		return auth.ErrAuthentication
	}
	return nil
}

func secretKey(scope, requestID string) string { return scope + "\x00" + requestID }

// readPrincipalJob is the shim's scoped read: another principal's job on the
// same account reads as absent, as it did before author scopes.
func (s *Store) readPrincipalJob(ctx context.Context, tx *sql.Tx, principal, binding, requestID string) (Job, error) {
	j, err := readJob(ctx, tx, s.principalScope(principal, binding), requestID)
	if err != nil {
		return Job{}, err
	}
	if j.Principal != principal || j.Binding != binding {
		return Job{}, ErrNotFound
	}
	return j, nil
}

func (s *Store) allowed(ctx context.Context, identity auth.Identity, r core.Request) error {
	if err := identity.Authorize(r); err != nil {
		return err
	}
	for _, p := range s.config.Principals {
		if p.ID == identity.ID() && p.Binding == identity.Binding() && !p.Revoked && slices.Contains(p.Operations, r.Operation) && (r.Target == nil || slices.Contains(p.Targets, *r.Target)) {
			var revoked bool
			if err := s.db.QueryRowContext(ctx, "SELECT revoked FROM authorization WHERE principal=? AND binding=?", p.ID, p.Binding).Scan(&revoked); err != nil {
				return err
			}
			if revoked {
				return auth.ErrForbidden
			}
			return nil
		}
	}
	return auth.ErrForbidden
}

// Admit atomically coalesces normalized explicit intent and persists its first
// effective defaults. replayUntil is trusted codec output (zero for sessions),
// never a caller-provided body field. Only mutations enter the delivery journal.
func (s *Store) Admit(ctx context.Context, identity auth.Identity, r core.Request, replayUntil int64, now time.Time) (Job, bool, error) {
	sub, err := s.principalSubmitter(identity)
	if err != nil {
		return Job{}, false, err
	}
	return s.admit(ctx, sub, &identity, r, replayUntil, now)
}

// AdmitAuthor admits a mutation posted as sub.Author. A guest posts under its
// nickname with no credential; a registered author's token stays in memory
// for its attempts and only its digest is journalled. AIRC, not the gateway,
// decides whether the author may post. Resubmitting the same request ID from
// any listener returns the same job; for a registered author it must carry a
// matching token, which also resumes a job waiting in credential_wait.
func (s *Store) AdmitAuthor(ctx context.Context, sub Submission, r core.Request, replayUntil int64, now time.Time) (Job, bool, error) {
	owner, err := s.authorSubmitter(sub)
	if err != nil {
		return Job{}, false, err
	}
	return s.admit(ctx, owner, nil, r, replayUntil, now)
}

func (s *Store) admit(ctx context.Context, sub submitter, identity *auth.Identity, r core.Request, replayUntil int64, now time.Time) (Job, bool, error) {
	if r.Target != nil {
		target := *r.Target
		r.Target = &target
	}
	if r.Profile != nil {
		profile := *r.Profile
		if profile.Model != nil {
			value := *profile.Model
			profile.Model = &value
		}
		if profile.Workspace != nil {
			value := *profile.Workspace
			profile.Workspace = &value
		}
		if profile.Tools != nil {
			value := *profile.Tools
			profile.Tools = &value
		}
		if profile.About != nil {
			value := *profile.About
			profile.About = &value
		}
		r.Profile = &profile
	}
	if r.Presence != nil {
		presence := *r.Presence
		r.Presence = &presence
	}
	if r.Topic != nil {
		value := *r.Topic
		r.Topic = &value
	}
	if r.Room != nil {
		room := *r.Room
		if room.SlowSeconds != nil {
			value := *room.SlowSeconds
			room.SlowSeconds = &value
		}
		if room.HistoryLimit != nil {
			value := *room.HistoryLimit
			room.HistoryLimit = &value
		}
		r.Room = &room
	}
	if r.ReplySignal != nil {
		value := *r.ReplySignal
		r.ReplySignal = &value
	}
	if r.Activity != nil {
		value := *r.Activity
		r.Activity = &value
	}
	if r.Poll != nil {
		value := *r.Poll
		value.Options = append([]string(nil), value.Options...)
		r.Poll = &value
	}
	if err := r.Normalize(); err != nil || !r.Operation.IsMutation() {
		return Job{}, false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, false, ErrClosed
	}
	if identity != nil {
		if err := s.allowed(ctx, *identity, r); err != nil {
			return Job{}, false, err
		}
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
	job, created, err := s.admitSubmitterInTx(ctx, tx, sub, r, replayUntil, n, fingerprint)
	if err != nil {
		return Job{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, false, err
	}
	return job, created, nil
}

// admitInTx requires the store lock, current authority, normalized request,
// checked time/replay bounds and an owned transaction. It never commits itself.
func (s *Store) admitInTx(ctx context.Context, tx *sql.Tx, identity auth.Identity, r core.Request, replayUntil, n int64, fingerprint string) (Job, bool, error) {
	sub, err := s.principalSubmitter(identity)
	if err != nil {
		return Job{}, false, err
	}
	return s.admitSubmitterInTx(ctx, tx, sub, r, replayUntil, n, fingerprint)
}

func (s *Store) admitSubmitterInTx(ctx context.Context, tx *sql.Tx, sub submitter, r core.Request, replayUntil, n int64, fingerprint string) (Job, bool, error) {
	var oldFingerprint string
	existing, err := readJob(ctx, tx, sub.scope, r.RequestID)
	if err == nil {
		if err := sub.owns(existing); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Job{}, false, ErrConflict
			}
			return Job{}, false, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT fingerprint FROM jobs WHERE author_scope=? AND request_id=?", sub.scope, r.RequestID).Scan(&oldFingerprint); err != nil {
			return Job{}, false, err
		}
		if oldFingerprint != fingerprint {
			return Job{}, false, ErrConflict
		}
		// Extend the marker if a later valid signature covers the same intent.
		if replayUntil > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO replay VALUES (?,?,?,?) ON CONFLICT(author_scope,request_id) DO UPDATE SET replay_until=max(replay_until,excluded.replay_until)`, sub.scope, r.RequestID, fingerprint, replayUntil); err != nil {
				return Job{}, false, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE jobs SET replay_until=max(replay_until,?) WHERE author_scope=? AND request_id=?", replayUntil, sub.scope, r.RequestID); err != nil {
				return Job{}, false, err
			}
		}
		if existing.Principal == "" && existing.Author.Registered {
			// The matching token is back in memory: a job parked by a restart resumes.
			s.secrets[secretKey(sub.scope, r.RequestID)] = sub.credential
			if _, err = tx.ExecContext(ctx, "UPDATE jobs SET state=resume_state,resume_state=NULL WHERE author_scope=? AND request_id=? AND state='credential_wait'", sub.scope, r.RequestID); err != nil {
				return Job{}, false, err
			}
		}
		job, err := readJob(ctx, tx, sub.scope, r.RequestID)
		if err != nil {
			return Job{}, false, err
		}
		return job, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Job{}, false, err
	}
	// A retained replay marker must not recreate a pruned result.
	var until int64
	err = tx.QueryRowContext(ctx, "SELECT replay_until FROM replay WHERE author_scope=? AND request_id=?", sub.scope, r.RequestID).Scan(&until)
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
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(`+laneSQL+`=?),0)
FROM jobs WHERE terminal_at IS NULL`, laneOf(sub.principal, sub.scope)).Scan(&total, &owned); err != nil {
		return Job{}, false, err
	}
	if total >= s.config.Limits.Pending || owned >= s.config.Limits.PendingPerPrincipal {
		return Job{}, false, ErrCapacity
	}
	upstream, err := core.UpstreamID(s.config.Realm, sub.scope, r.RequestID)
	if err != nil {
		return Job{}, false, ErrInvalid
	}
	data, err := json.Marshal(r)
	if err != nil {
		return Job{}, false, err
	}
	lookupLimit := s.config.Retry.LookupAttempts
	if r.Operation == core.ProfileUpdate || r.Operation == core.PresenceSet || r.Operation == core.TopicSet || r.Operation == core.RoomUpdate || r.Operation == core.PinsAdd || r.Operation == core.PinsRemove || r.Operation == core.ActivitySet || r.Operation == core.ReplySignalSet || r.Operation == core.ReplySignalCancel || r.Operation == core.PollCreate || r.Operation == core.PollVote || r.Operation == core.PollClose || r.Operation == core.MessageAction || r.Operation == core.MessageCorrect || r.Operation == core.MessageRetract {
		lookupLimit = 0
	}
	var check any
	if tokenCheck := sub.tokenCheck(); tokenCheck != "" {
		check = tokenCheck
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs (author_scope,request_id,author_nick,registered,listener,token_check,principal,binding,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'queued',?,?,?,?,?,?,?,?,?,?)`, sub.scope, r.RequestID, sub.author.Nick, sub.author.Registered, sub.listener, check, sub.principal, sub.binding, fingerprint, upstream, data, policy, n, expires, replayUntil, s.config.Retry.TerminalRetentionSeconds, s.config.Retry.SendAttempts, lookupLimit, s.config.Retry.BackoffSeconds, s.config.Retry.MaxBackoffSeconds, n, n)
	if err != nil {
		return Job{}, false, err
	}
	if replayUntil > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO replay VALUES (?,?,?,?) ON CONFLICT(author_scope,request_id) DO UPDATE SET fingerprint=excluded.fingerprint,replay_until=excluded.replay_until`, sub.scope, r.RequestID, fingerprint, replayUntil); err != nil {
			return Job{}, false, err
		}
	}
	if check != nil {
		s.secrets[secretKey(sub.scope, r.RequestID)] = sub.credential
	}
	job, err := readJob(ctx, tx, sub.scope, r.RequestID)
	if err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func readJob(ctx context.Context, tx *sql.Tx, scope, requestID string) (Job, error) {
	if scope == "" {
		return Job{}, ErrNotFound
	}
	j := Job{Scope: scope, Result: core.Result{Version: 1, RequestID: requestID, Durable: true}}
	var tokenCheck, accountID sql.NullString
	var data []byte
	var receipt []byte
	var cardOutcome []byte
	var topicOutcome []byte
	var roomOutcome []byte
	var pinOutcome []byte
	var activityOutcome []byte
	var replySignalOutcome []byte
	var pollOutcome []byte
	var message, failure, activeKind sql.NullString
	var accepted, persisted, connected sql.NullBool
	var transient bool
	var upstreamCode, sendLimit, lookupLimit, lookupForSend int
	var nextSend, nextLookup int64
	err := tx.QueryRowContext(ctx, `SELECT author_nick,registered,listener,token_check,account_id,principal,binding,upstream_id,request,policy,state,expires_at,replay_until,send_attempts,lookup_attempts,accepted,persisted,recipient_connected,message_id,duplicate_risk,last_error,receipt,card_outcome,topic_outcome,room_outcome,pin_outcome,activity_outcome,reply_signal_outcome,poll_outcome,may_have_accepted,failure_transient,upstream_error,send_limit,lookup_limit,next_send_at,next_lookup_at,lookup_for_send,attempt_kind FROM jobs WHERE author_scope=? AND request_id=?`, scope, requestID).Scan(&j.Author.Nick, &j.Author.Registered, &j.Listener, &tokenCheck, &accountID, &j.Principal, &j.Binding, &j.UpstreamID, &data, &j.Policy, &j.Result.State, &j.Result.ExpiresAt, &j.ReplayUntil, &j.Result.SendAttempts, &j.Result.LookupAttempts, &accepted, &persisted, &connected, &message, &j.Result.DuplicateRisk, &failure, &receipt, &cardOutcome, &topicOutcome, &roomOutcome, &pinOutcome, &activityOutcome, &replySignalOutcome, &pollOutcome, &j.Result.AcceptanceUncertain, &transient, &upstreamCode, &sendLimit, &lookupLimit, &nextSend, &nextLookup, &lookupForSend, &activeKind)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	j.tokenCheck, j.AccountID = tokenCheck.String, accountID.String
	if err = json.Unmarshal(data, &j.Request); err != nil {
		return Job{}, err
	}
	if receipt != nil && (cardOutcome != nil || topicOutcome != nil || roomOutcome != nil || pinOutcome != nil || activityOutcome != nil || replySignalOutcome != nil || pollOutcome != nil) || cardOutcome != nil && (topicOutcome != nil || roomOutcome != nil || pinOutcome != nil || activityOutcome != nil || replySignalOutcome != nil || pollOutcome != nil) || topicOutcome != nil && (roomOutcome != nil || pinOutcome != nil || activityOutcome != nil || replySignalOutcome != nil || pollOutcome != nil) || roomOutcome != nil && (pinOutcome != nil || activityOutcome != nil || replySignalOutcome != nil || pollOutcome != nil) || pinOutcome != nil && (activityOutcome != nil || replySignalOutcome != nil || pollOutcome != nil) || activityOutcome != nil && (replySignalOutcome != nil || pollOutcome != nil) || replySignalOutcome != nil && pollOutcome != nil {
		return Job{}, ErrInvalid
	}
	if receipt != nil {
		j.Receipt = &irc.SendReceiptEvent{}
		if err := json.Unmarshal(receipt, j.Receipt); err != nil {
			return Job{}, err
		}
	}
	if cardOutcome != nil {
		j.Result.Card = &core.CardAcknowledgement{}
		if err := json.Unmarshal(cardOutcome, j.Result.Card); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
	}
	if topicOutcome != nil {
		j.Result.Topic = &core.TopicAcknowledgement{}
		if err := json.Unmarshal(topicOutcome, j.Result.Topic); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
	}
	if roomOutcome != nil {
		j.Result.Room = &core.RoomAcknowledgement{}
		if err := json.Unmarshal(roomOutcome, j.Result.Room); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
	}
	if pinOutcome != nil {
		j.Result.Pin = &core.PinAcknowledgement{}
		if err := json.Unmarshal(pinOutcome, j.Result.Pin); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
	}
	if activityOutcome != nil {
		j.Result.Activity = &core.ActivityAcknowledgement{}
		if err := json.Unmarshal(activityOutcome, j.Result.Activity); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
	}
	if replySignalOutcome != nil {
		j.Result.ReplySignal = &core.ReplySignalAcknowledgement{}
		if err := json.Unmarshal(replySignalOutcome, j.Result.ReplySignal); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
	}
	if pollOutcome != nil {
		j.Result.Poll = &core.PollAcknowledgement{}
		if err := json.Unmarshal(pollOutcome, j.Result.Poll); err != nil {
			return Job{}, err
		}
		j.Result.Persisted = nil
		j.Result.RecipientConnected = nil
		j.Result.MessageID = ""
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
	if j.Result.Poll != nil && j.Result.Poll.Operation == core.PollCreate && j.Result.Poll.Receipt != nil && j.Result.Poll.Receipt.Receipt != nil {
		persisted := j.Result.Poll.Receipt.Receipt.Persisted
		j.Result.Persisted = &persisted
		if connected := j.Result.Poll.Receipt.Receipt.RecipientConnected; connected != nil {
			value := *connected
			j.Result.RecipientConnected = &value
		}
		j.Result.MessageID = j.Result.Poll.Receipt.ID
	}
	if j.Result.State == core.Dispatching {
		j.Result.AcceptanceUncertain = true
	}
	setNextAction(&j, sendLimit, lookupLimit, nextSend, nextLookup, lookupForSend)
	if activeKind.String == string(LookupAttempt) {
		j.Result.NextAction, j.Result.NextAttemptAt = "await_lookup", nil
	}
	if failure.Valid {
		j.Result.Failure = &core.Failure{Code: core.ErrorCode(failure.String), Transient: transient, UpstreamCode: upstreamCode}
	}
	return j, nil
}

// Lookup is local and scoped; it does not reserve a probe, extend expiry or send.
func (s *Store) Lookup(ctx context.Context, identity auth.Identity, requestID string) (Job, error) {
	sub, err := s.principalSubmitter(identity)
	if err != nil {
		return Job{}, err
	}
	return s.lookup(ctx, sub, &identity, requestID)
}

// LookupAuthor reads an author's job by request ID from any listener. A
// registered author's job is visible only with a matching credential.
func (s *Store) LookupAuthor(ctx context.Context, sub Submission, requestID string) (Job, error) {
	owner, err := s.authorSubmitter(sub)
	if err != nil {
		return Job{}, err
	}
	return s.lookup(ctx, owner, nil, requestID)
}

func (s *Store) lookup(ctx context.Context, sub submitter, identity *auth.Identity, requestID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if identity != nil {
		r := core.Request{Version: 1, RequestID: "status", Operation: core.ReceiptGet, LookupID: requestID}
		if err := s.allowed(ctx, *identity, r); err != nil {
			return Job{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, sub.scope, requestID)
	if err != nil {
		return Job{}, err
	}
	if err := sub.owns(j); err != nil {
		return Job{}, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}
