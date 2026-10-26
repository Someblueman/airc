package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/protocol"
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
type Job struct {
	Author      core.Author
	Scope       string
	Listener    string
	AccountID   string
	UpstreamID  string
	Request     core.Request
	Policy      core.Policy
	Result      core.Result
	ReplayUntil int64
	Receipt     *irc.SendReceiptEvent
	tokenCheck  string
}

// Lane is the fairness and session-limit owner: the author scope.
func (j Job) Lane() string { return j.Scope }

// Submission is an author-level admission or lookup: who posts, the in-memory
// AIRC account token for a registered author (never stored), and the listener
// that received it. Listener is informational: identity, deduplication and
// cursors never depend on it, so a client may fall back between transports.
type Submission struct {
	Author     core.Author
	Credential core.Credential
	Listener   string
	// Source is the client's address (no port) or a listener label, kept in
	// memory only and forwarded to AIRC for its per-source limits.
	Source string
}

// submitter is the resolved owner of an admission or lookup.
type submitter struct {
	author     core.Author
	scope      string
	listener   string
	source     string
	credential core.Credential
}

func (sub submitter) tokenCheck() string {
	if !sub.author.Registered {
		return ""
	}
	return sub.credential.Check(sub.author.Nick)
}

func (s *Store) authorSubmitter(sub Submission) (submitter, error) {
	if !sub.Author.Valid() || len(sub.Listener) > 64 || sub.Source != "" && !protocol.ValidSource(sub.Source) || sub.Author.Registered == sub.Credential.Empty() {
		return submitter{}, ErrInvalid
	}
	if _, ok := s.config.AuthorUpstream(); !ok {
		return submitter{}, auth.ErrForbidden
	}
	return submitter{author: sub.Author, scope: sub.Author.Scope(), listener: sub.Listener, source: sub.Source, credential: sub.Credential}, nil
}

// owns decides whether sub may see or resubmit an existing job. A
// registered author's job requires a credential matching the stored check; a
// guest job is anyone using that nickname, exactly as AIRC treats guests.
func (sub submitter) owns(j Job) error {
	if j.Author.Registered && !sub.credential.MatchesCheck(j.Author.Nick, j.tokenCheck) {
		return auth.ErrAuthentication
	}
	return nil
}

func secretKey(scope, requestID string) string { return scope + "\x00" + requestID }

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
	job, created, _, err := s.admit(ctx, owner, r, replayUntil, false, now)
	return job, created, err
}

// AdmitAuthorAwait is AdmitAuthor that then waits up to wait (or until ctx
// ends) for the job's next saved outcome, so a caller can answer with the
// delivered result instead of a queued one. A job that is not queued or
// dispatching is returned at once; a wait that ends first returns the job
// as admitted. The wait holds no store lock.
func (s *Store) AdmitAuthorAwait(ctx context.Context, sub Submission, r core.Request, wait time.Duration, now time.Time) (Job, error) {
	owner, err := s.authorSubmitter(sub)
	if err != nil {
		return Job{}, err
	}
	job, _, outcome, err := s.admit(ctx, owner, r, 0, wait > 0, now)
	if err != nil || outcome == nil {
		return job, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case saved := <-outcome:
		return saved, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	s.unwatch(secretKey(job.Scope, job.Request.RequestID), outcome)
	// An outcome saved between the timeout and unwatch is still the newer
	// answer.
	select {
	case saved := <-outcome:
		return saved, nil
	default:
		return job, nil
	}
}

// admit journals r. With await, a job that is queued or dispatching also
// gets an outcome channel, registered before the store lock is released so
// no outcome saved after this admission can be missed.
func (s *Store) admit(ctx context.Context, sub submitter, r core.Request, replayUntil int64, await bool, now time.Time) (Job, bool, chan Job, error) {
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
		return Job{}, false, nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, false, nil, ErrClosed
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return Job{}, false, nil, ErrInvalid
	}
	n := now.Unix()
	if n < 0 || n > math.MaxInt64-s.config.Retry.LifetimeSeconds-auth.ClockSkewSeconds || replayUntil < 0 {
		return Job{}, false, nil, ErrInvalid
	}
	if replayUntil > 0 && (r.IssuedAt == nil || r.ExpiresAt == nil || *r.ExpiresAt > math.MaxInt64-auth.ClockSkewSeconds || replayUntil != *r.ExpiresAt+auth.ClockSkewSeconds) {
		return Job{}, false, nil, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return Job{}, false, nil, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Job{}, false, nil, err
	}
	defer tx.Rollback()
	job, created, err := s.admitSubmitterInTx(ctx, tx, sub, r, replayUntil, n, fingerprint)
	if err != nil {
		return Job{}, false, nil, err
	}
	if err = commit(ctx, tx); err != nil {
		return Job{}, false, nil, err
	}
	var outcome chan Job
	if await && (job.Result.State == core.Queued || job.Result.State == core.Dispatching) {
		outcome = s.watch(secretKey(job.Scope, job.Request.RequestID))
	}
	return job, created, outcome, nil
}

func (s *Store) admitSubmitterInTx(ctx context.Context, tx *txn, sub submitter, r core.Request, replayUntil, n int64, fingerprint string) (Job, bool, error) {
	// Probe with the narrow fingerprint query: most admissions are new, and
	// the full job read costs far more than this under the store lock.
	var oldFingerprint string
	err := tx.QueryRowContext(ctx, "SELECT fingerprint FROM jobs WHERE author_scope=? AND request_id=?", sub.scope, r.RequestID).Scan(&oldFingerprint)
	if err == nil {
		existing, err := readJob(ctx, tx, sub.scope, r.RequestID)
		if err != nil {
			return Job{}, false, err
		}
		if err := sub.owns(existing); err != nil {
			return Job{}, false, err
		}
		if oldFingerprint != fingerprint {
			return Job{}, false, ErrConflict
		}
		// Extend the replay marker if a later admission covers the same intent.
		if replayUntil > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO replay VALUES (?,?,?,?) ON CONFLICT(author_scope,request_id) DO UPDATE SET replay_until=max(replay_until,excluded.replay_until)`, sub.scope, r.RequestID, fingerprint, replayUntil); err != nil {
				return Job{}, false, err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE jobs SET replay_until=max(replay_until,?) WHERE author_scope=? AND request_id=?", replayUntil, sub.scope, r.RequestID); err != nil {
				return Job{}, false, err
			}
		}
		if sub.source != "" {
			s.sources[secretKey(sub.scope, r.RequestID)] = sub.source
		}
		if existing.Author.Registered {
			// The matching token is back in memory: a job parked by a restart resumes.
			s.secrets[secretKey(sub.scope, r.RequestID)] = sub.credential
			resumed, err := tx.ExecContext(ctx, "UPDATE jobs SET state=resume_state,resume_state=NULL WHERE author_scope=? AND request_id=? AND state='credential_wait'", sub.scope, r.RequestID)
			if err != nil {
				return Job{}, false, err
			}
			if count, err := resumed.RowsAffected(); err != nil {
				return Job{}, false, err
			} else if count > 0 {
				s.signalAdmitted()
			}
		}
		job, err := readJob(ctx, tx, sub.scope, r.RequestID)
		if err != nil {
			return Job{}, false, err
		}
		return job, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
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
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(author_scope=?),0)
FROM jobs WHERE terminal_at IS NULL`, sub.scope).Scan(&total, &owned); err != nil {
		return Job{}, false, err
	}
	if total >= s.config.Limits.Pending || owned >= s.config.Limits.PendingPerAuthor {
		return Job{}, false, ErrCapacity
	}
	upstream, err := core.UpstreamID(sub.scope, r.RequestID)
	if err != nil {
		return Job{}, false, ErrInvalid
	}
	data, err := json.Marshal(r)
	if err != nil {
		return Job{}, false, err
	}
	lookupLimit := 0
	if r.Operation.HasLookup() {
		lookupLimit = s.config.Retry.LookupAttempts
	}
	var check any
	if tokenCheck := sub.tokenCheck(); tokenCheck != "" {
		check = tokenCheck
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs (author_scope,request_id,author_nick,registered,listener,token_check,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES (?,?,?,?,?,?,?,?,?,?,'queued',?,?,?,?,?,?,?,?,?,?)`, sub.scope, r.RequestID, sub.author.Nick, sub.author.Registered, sub.listener, check, fingerprint, upstream, data, policy, n, expires, replayUntil, s.config.Retry.TerminalRetentionSeconds, s.config.Retry.SendAttempts, lookupLimit, s.config.Retry.BackoffSeconds, s.config.Retry.MaxBackoffSeconds, n, n)
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
	if sub.source != "" {
		s.sources[secretKey(sub.scope, r.RequestID)] = sub.source
	}
	job, err := readJob(ctx, tx, sub.scope, r.RequestID)
	if err != nil {
		return Job{}, false, err
	}
	s.signalAdmitted()
	return job, true, nil
}

func readJob(ctx context.Context, tx *txn, scope, requestID string) (Job, error) {
	if scope == "" {
		return Job{}, ErrNotFound
	}
	j := Job{Scope: scope, Result: core.Result{Version: 1, RequestID: requestID, Durable: true}}
	var tokenCheck, accountID sql.NullString
	var data []byte
	var outcome []byte
	var failure, activeKind sql.NullString
	var accepted sql.NullBool
	var transient bool
	var upstreamCode, sendLimit, lookupLimit, lookupForSend int
	var nextSend, nextLookup int64
	err := tx.QueryRowContext(ctx, `SELECT author_nick,registered,listener,token_check,account_id,upstream_id,request,policy,state,expires_at,replay_until,send_attempts,lookup_attempts,accepted,duplicate_risk,last_error,outcome,may_have_accepted,failure_transient,upstream_error,send_limit,lookup_limit,next_send_at,next_lookup_at,lookup_for_send,attempt_kind FROM jobs WHERE author_scope=? AND request_id=?`, scope, requestID).Scan(&j.Author.Nick, &j.Author.Registered, &j.Listener, &tokenCheck, &accountID, &j.UpstreamID, &data, &j.Policy, &j.Result.State, &j.Result.ExpiresAt, &j.ReplayUntil, &j.Result.SendAttempts, &j.Result.LookupAttempts, &accepted, &j.Result.DuplicateRisk, &failure, &outcome, &j.Result.AcceptanceUncertain, &transient, &upstreamCode, &sendLimit, &lookupLimit, &nextSend, &nextLookup, &lookupForSend, &activeKind)
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
	if outcome != nil {
		evidence, err := core.DecodeAcceptance(j.Request.Operation, outcome)
		if err != nil {
			return Job{}, err
		}
		j.Receipt = evidence.Receipt
		j.Result.Card, j.Result.Topic, j.Result.Room, j.Result.Pin = evidence.Card, evidence.Topic, evidence.Room, evidence.Pin
		j.Result.Activity, j.Result.ReplySignal, j.Result.Poll = evidence.Activity, evidence.ReplySignal, evidence.Poll
		if r := evidence.MessageReceipt(); r != nil && r.Receipt != nil {
			persisted := r.Receipt.Persisted
			j.Result.Persisted = &persisted
			if connected := r.Receipt.RecipientConnected; connected != nil {
				value := *connected
				j.Result.RecipientConnected = &value
			}
			if active := r.Receipt.RecipientActive; active != nil {
				value := *active
				j.Result.RecipientActive = &value
			}
			j.Result.MessageID = r.ID
		}
	}
	if accepted.Valid {
		j.Result.Accepted = &accepted.Bool
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

// LookupAuthor reads an author's job by request ID from any listener. A
// registered author's job is visible only with a matching credential.
func (s *Store) LookupAuthor(ctx context.Context, sub Submission, requestID string) (Job, error) {
	owner, err := s.authorSubmitter(sub)
	if err != nil {
		return Job{}, err
	}
	return s.lookup(ctx, owner, requestID)
}

func (s *Store) lookup(ctx context.Context, sub submitter, requestID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	tx, err := s.begin(ctx)
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
	if err = commit(ctx, tx); err != nil {
		return Job{}, err
	}
	return j, nil
}
