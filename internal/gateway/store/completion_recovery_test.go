//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCrashedLookupCannotRepeatBeforeSameResend(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, _ := consumeAttempt(t, s, identity.ID(), SendAttempt, now)
	j, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Unix(*j.Result.NextAttemptAt, 0)
	consumeAttempt(t, s, identity.ID(), LookupAttempt, now)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Reserve(ctx, identity.ID(), "request", LookupAttempt, now); !errors.Is(err, ErrNotReady) {
		t.Fatal("restart allowed second probe before same resend", err)
	}
	_, a := consumeAttempt(t, s, identity.ID(), SendAttempt, now)
	if a.Job.Result.SendAttempts != 2 || a.Job.Result.LookupAttempts != 1 || !a.Job.Result.AcceptanceUncertain {
		t.Fatal("restart reset counters or uncertainty", a)
	}
}

func TestUnknownLookupDoesNotUndoPermanentSendRejection(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	// Restart a send marker: its original acceptance is unknown.
	consumeAttempt(t, s, identity.ID(), SendAttempt, now)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := consumeAttempt(t, s, identity.ID(), SendAttempt, now)
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.InvalidRequest}}, now); err != nil {
		t.Fatal(err)
	}
	lg, _ := consumeAttempt(t, s, identity.ID(), LookupAttempt, now)
	j, err := s.Complete(ctx, lg, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true, UpstreamCode: 488}}, now)
	if err != nil || j.Result.State != core.Rejected || !j.Result.AcceptanceUncertain || j.Result.NextAction != "lookup" {
		t.Fatal("lookup reopened rejected send", j, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now.Add(time.Hour)); !errors.Is(err, ErrNotReady) {
		t.Fatal("restart undid permanent send rejection", err)
	}
}
