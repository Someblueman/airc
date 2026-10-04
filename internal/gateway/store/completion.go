package store

import (
	"context"
	"database/sql"
	"math"
	"math/rand/v2"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// Completion is trusted backend evidence, not an ingress argument. MayHavePosted
// describes this send only; earlier uncertainty is retained independently.
// A lookup cannot post. Unknown retained receipts use DeliveryUncertain.
type Completion struct {
	Failure           core.Failure
	MayHavePosted     bool
	RetryAfterSeconds int64
}

func (c Completion) valid(kind AttemptKind) bool {
	if c.RetryAfterSeconds < 0 || c.Failure.UpstreamCode < 0 || c.Failure.UpstreamCode > 999 || kind == LookupAttempt && c.MayHavePosted {
		return false
	}
	switch c.Failure.Code {
	case core.UpstreamUnavailable, core.CapacityExceeded, core.DeliveryUncertain:
		return c.Failure.Transient
	case core.AuthenticationFailed, core.Forbidden, core.UnsupportedOperation, core.InvalidRequest, core.IDConflict:
		return !c.Failure.Transient && !c.MayHavePosted && c.RetryAfterSeconds == 0
	}
	return false
}

// retryDelay uses the stored policy. Jitter is bounded to [ceil(cap/2), cap];
// a valid server delay may exceed the configured exponential cap.
func retryDelay(attempts, base, limit int, serverDelay int64) int64 {
	delay := base
	for i := 1; i < attempts && delay < limit; i++ {
		delay = min(delay*2, limit)
	}
	low := (delay + 1) / 2
	return max(int64(low+rand.IntN(delay-low+1)), serverDelay)
}

// Complete records a failed bounded attempt after the backend has closed its
// session and joined its work. It never authorizes network I/O. Save failures
// retain the active marker, permitting only another completion save. A privately
// observed acceptance must instead be saved with RetryAcceptance.
func (s *Store) Complete(ctx context.Context, g *Grant, c Completion, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used || g.state.acceptance != nil || g.state.cardAcceptance != nil || g.state.topicAcceptance != nil || g.state.roomAcceptance != nil || g.state.pinAcceptance != nil || g.state.activityAcceptance != nil || g.state.replySignalAcceptance != nil || g.state.pollAcceptance != nil {
		return Job{}, ErrNotReady
	}
	a := g.state.attempt
	n := now.Unix()
	if now.Before(g.state.consumedAt) || n < 0 || n > math.MaxInt64-86400 || !c.valid(a.Kind) {
		return Job{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, a.Job.Principal, a.Job.Binding, a.Job.Request.RequestID)
	if err != nil {
		return Job{}, err
	}
	// A saved acceptance is authoritative even if completion arrives late.
	if j.Result.State == core.Accepted {
		return j, tx.Commit()
	}
	var active, kind sql.NullString
	var may, sendBlocked bool
	var sendLimit, base, cap int
	var nextSend, nextLookup int64
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id,attempt_kind,may_have_accepted,send_blocked,send_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at FROM jobs WHERE principal=? AND binding=? AND request_id=?`, j.Principal, j.Binding, j.Request.RequestID).Scan(&active, &kind, &may, &sendBlocked, &sendLimit, &base, &cap, &nextSend, &nextLookup); err != nil {
		return Job{}, err
	}
	if !active.Valid || active.String != a.ID || kind.String != string(a.Kind) {
		return Job{}, ErrStaleAttempt
	}
	may = may || c.MayHavePosted
	state := core.Uncertain
	var terminal any
	if !c.Failure.Transient {
		state = core.Rejected
		sendBlocked = true
		if c.Failure.Code == core.AuthenticationFailed || c.Failure.Code == core.Forbidden {
			state = core.Blocked
		}
		if !may {
			terminal = n
		}
	} else if j.Result.ExpiresAt <= n {
		state = core.Expired
		if !may {
			terminal = n
		}
	} else {
		count := j.Result.LookupAttempts
		if a.Kind == SendAttempt {
			count = j.Result.SendAttempts
		}
		// Clamp even an enormous upstream delay without overflowing or extending expiry.
		delay := min(retryDelay(count, base, cap, c.RetryAfterSeconds), j.Result.ExpiresAt-n)
		if a.Kind == SendAttempt {
			nextSend, nextLookup = n+delay, n+delay
		} else {
			nextLookup = n + delay
			nextSend = max(nextSend, n+min(c.RetryAfterSeconds, j.Result.ExpiresAt-n))
		}
		if j.Policy == core.Retry && j.Result.SendAttempts < sendLimit {
			state = core.RetryWait
		}
		if sendBlocked {
			state = core.Rejected
		}
		if !may && (sendBlocked || j.Result.SendAttempts >= sendLimit || j.Policy == core.ReceiptOnly) {
			state, terminal = core.Rejected, n
		}
	}
	var accepted any = false
	if may {
		accepted = nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,accepted=?,may_have_accepted=?,send_blocked=?,last_error=?,failure_transient=?,upstream_error=?,next_send_at=?,next_lookup_at=?,lookup_for_send=CASE WHEN attempt_kind='lookup' THEN send_attempts ELSE lookup_for_send END,terminal_at=?,attempt_id=NULL,attempt_kind=NULL WHERE principal=? AND binding=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, state, accepted, may, sendBlocked, c.Failure.Code, c.Failure.Transient, c.Failure.UpstreamCode, nextSend, nextLookup, terminal, j.Principal, j.Binding, j.Request.RequestID, a.ID, a.Kind)
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
	j, err = readJob(ctx, tx, j.Principal, j.Binding, j.Request.RequestID)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return j, nil
}
