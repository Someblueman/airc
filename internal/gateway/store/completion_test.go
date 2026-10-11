//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func consumeAttempt(t *testing.T, s *Store, scope string, kind AttemptKind, now time.Time) (*Grant, Attempt) {
	t.Helper()
	g, err := s.Reserve(context.Background(), scope, "request", kind, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	return g, a
}

func TestCompletionKnownNoWriteAndPersistentDelay(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, _ := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
	f := Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}, RetryAfterSeconds: 10}
	j, err := s.Complete(ctx, g, f, now)
	if err != nil || j.Result.State != core.RetryWait || j.Result.AcceptanceUncertain || j.Result.Accepted == nil || *j.Result.Accepted || j.Result.NextAction != "send" || j.Result.NextAttemptAt == nil || *j.Result.NextAttemptAt != 1010 {
		t.Fatal(j, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Retry.BackoffSeconds = 30 // Does not replace the admitted job's policy.
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err = s.LookupAuthor(ctx, identity, "request")
	if err != nil || !j.Result.Failure.Transient || *j.Result.NextAttemptAt != 1010 {
		t.Fatal("schedule lost", j, err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, time.Unix(1009, 0)); !errors.Is(err, ErrNotReady) {
		t.Fatal("retry ignored delay", err)
	}
	g, a := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, time.Unix(1010, 0))
	if a.Job.Result.DuplicateRisk {
		t.Fatal("known no-write became duplicate risk")
	}
	j, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.InvalidRequest, UpstreamCode: 412}}, time.Unix(1010, 0))
	if err != nil || j.Result.State != core.Rejected || j.Result.AcceptanceUncertain || j.Result.Failure.UpstreamCode != 412 {
		t.Fatal(j, err)
	}
}

func TestCompletionLookupFallbackAndEarlierUncertainty(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, a := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
	j, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now)
	if err != nil || !j.Result.AcceptanceUncertain || j.Result.Accepted != nil || j.Result.NextAction != "lookup" {
		t.Fatal(j, err)
	}
	now = time.Unix(*j.Result.NextAttemptAt, 0)
	lg, _ := consumeAttempt(t, s, scopeOf(s, identity), LookupAttempt, now)
	j, err = s.Complete(ctx, lg, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true, UpstreamCode: 488}}, now)
	if err != nil || j.Result.NextAction != "send" || !j.Result.AcceptanceUncertain {
		t.Fatal(j, err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, now.Add(time.Minute)); !errors.Is(err, ErrNotReady) {
		t.Fatal("multiple probes before resend", err)
	}
	g, again := consumeAttempt(t, s, scopeOf(s, identity), SendAttempt, now)
	if again.Job.UpstreamID != a.Job.UpstreamID || !again.Job.Result.DuplicateRisk {
		t.Fatal("resend changed identity or lost risk")
	}
	j, err = s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.InvalidRequest, UpstreamCode: 412}}, now)
	if err != nil || j.Result.State != core.Rejected || !j.Result.AcceptanceUncertain || j.Result.Accepted != nil || j.Result.NextAction != "lookup" {
		t.Fatal("later rejection erased possible prior acceptance", j, err)
	}
	if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("permanent rejection resent", err)
	}
	now = time.Unix(*j.Result.NextAttemptAt, 0)
	lg, la := consumeAttempt(t, s, scopeOf(s, identity), LookupAttempt, now)
	j, err = s.RecordAcceptance(ctx, lg, receiptFor(la), now, now)
	if err != nil || j.Result.AcceptanceUncertain || j.Result.State != core.Accepted {
		t.Fatal("retained receipt failed to resolve rejection uncertainty", j, err)
	}
}
