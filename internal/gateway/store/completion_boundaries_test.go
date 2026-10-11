//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCompletionPoliciesBudgetsAndExpiry(t *testing.T) {
	for _, policy := range []core.Policy{core.Retry, core.ReceiptOnly} {
		t.Run(string(policy), func(t *testing.T) {
			c, identity, _ := fixture(t)
			c.Retry.Policy, c.Retry.SendAttempts, c.Retry.LookupAttempts = policy, 1, 2
			ctx := context.Background()
			now := time.Unix(1000, 0)
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := request()
			expires := int64(1010)
			r.ExpiresAt = &expires
			if _, _, err := s.Admit(ctx, identity, r, 0, now); err != nil {
				t.Fatal(err)
			}
			g, _ := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
			j, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now)
			if err != nil || j.Result.State != core.Uncertain || j.Result.NextAction != "lookup" {
				t.Fatal(j, err)
			}
			now = time.Unix(*j.Result.NextAttemptAt, 0)
			lg, _ := consumeAttempt(t, s, scopeOf(s, identity), LookupAttempt, now)
			j, err = s.Complete(ctx, lg, Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}, RetryAfterSeconds: math.MaxInt64}, now)
			if err != nil || j.Result.ExpiresAt != expires || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 1 {
				t.Fatal("delay extended lifetime or budget", j, err)
			}
			if policy == core.ReceiptOnly && (j.Result.NextAttemptAt == nil || *j.Result.NextAttemptAt != expires) {
				t.Fatal("lookup delay not capped at expiry", j)
			}
			if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
				t.Fatal("resend escaped policy/budget", err)
			}
			if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, time.Unix(expires, 0)); !errors.Is(err, ErrNotReady) {
				t.Fatal("lookup escaped expiry", err)
			}
		})
	}
}

func TestCompletionSaveFailureAndAcceptanceDominance(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, a := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
	f := Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}, MayHavePosted: true}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fixture_completion BEFORE UPDATE OF last_error ON jobs BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ctx, g, f, now); err == nil {
		t.Fatal("failed save reported completion")
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("failed completion released marker", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fixture_completion"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fixture_receipt BEFORE UPDATE OF receipt ON jobs BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now); err == nil {
		t.Fatal("failed receipt save reported success")
	}
	if _, err := s.Complete(ctx, g, f, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("completion erased observed acceptance", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fixture_receipt"); err != nil {
		t.Fatal(err)
	}
	j, err := s.RetryAcceptance(ctx, g, now)
	if err != nil || j.Result.State != core.Accepted {
		t.Fatal(j, err)
	}
	if _, err := s.Complete(ctx, g, f, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("grant holding receipt allowed failure completion", err)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	for attempts := 1; attempts <= 100; attempts++ {
		cap := 1
		for i := 1; i < attempts; i++ {
			cap = min(2*cap, 60)
		}
		for i := 0; i < 100; i++ {
			d := retryDelay(attempts, 1, 60, 0)
			if d < int64((cap+1)/2) || d > int64(cap) {
				t.Fatal("jitter escaped cap", attempts, d)
			}
		}
	}
	if retryDelay(100, 1, 60, 120) != 120 {
		t.Fatal("server delay lost")
	}
}
