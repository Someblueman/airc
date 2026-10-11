//go:build darwin || linux

package store

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestReadyFairRotationActiveBackoffAndRevocation(t *testing.T) {
	c, first, second := fixture(t)
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	for i := range 3 {
		r := request()
		r.RequestID = []string{"a", "b", "c"}[i]
		if _, _, err := s.Admit(ctx, first, r, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Admit(ctx, second, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	due, err := s.Ready(ctx, "", 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != "p:"+first.ID() || due[0].RequestID != "a" || due[0].Kind != SendAttempt {
		t.Fatal("initial due selection", due, err)
	}
	due, err = s.Ready(ctx, "p:"+first.ID(), 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != "p:"+second.ID() {
		t.Fatal("flood starved next principal", due, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, second), request().RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	due, err = s.Ready(ctx, "p:"+first.ID(), 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != "p:"+first.ID() {
		t.Fatal("active attempt selected", due, err)
	}
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now); err != nil {
		t.Fatal(err)
	}
	due, err = s.Ready(ctx, "p:"+first.ID(), 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != "p:"+first.ID() {
		t.Fatal("backoff ignored", due, err)
	}
	due, err = s.Ready(ctx, "p:"+first.ID(), 1, now.Add(2*time.Second))
	if err != nil || len(due) != 1 || due[0].Lane != "p:"+second.ID() || due[0].Kind != LookupAttempt {
		t.Fatal("uncertain resend skipped receipt recovery", due, err)
	}
	if _, err := s.Revoke(ctx, second.ID()); err != nil {
		t.Fatal(err)
	}
	due, err = s.Ready(ctx, "p:"+first.ID(), 1, now.Add(2*time.Second))
	if err != nil || len(due) != 1 || due[0].Lane != "p:"+first.ID() {
		t.Fatal("revoked work selected", due, err)
	}
}
