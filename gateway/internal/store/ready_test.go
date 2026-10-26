//go:build darwin || linux

package store

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestReadyFairRotationActiveAndBackoff(t *testing.T) {
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
		if _, _, err := s.AdmitAuthor(ctx, first, r, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.AdmitAuthor(ctx, second, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	due, err := s.Ready(ctx, "", 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != first.Author.Scope() || due[0].RequestID != "a" || due[0].Kind != SendAttempt {
		t.Fatal("initial due selection", due, err)
	}
	due, err = s.Ready(ctx, first.Author.Scope(), 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != second.Author.Scope() {
		t.Fatal("flood starved next author", due, err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, second), request().RequestID, SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(now); err != nil {
		t.Fatal(err)
	}
	due, err = s.Ready(ctx, first.Author.Scope(), 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != first.Author.Scope() {
		t.Fatal("active attempt selected", due, err)
	}
	if _, err := s.Complete(ctx, g, Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true}, MayHavePosted: true}, now); err != nil {
		t.Fatal(err)
	}
	due, err = s.Ready(ctx, first.Author.Scope(), 1, now)
	if err != nil || len(due) != 1 || due[0].Lane != first.Author.Scope() {
		t.Fatal("backoff ignored", due, err)
	}
	due, err = s.Ready(ctx, first.Author.Scope(), 1, now.Add(2*time.Second))
	if err != nil || len(due) != 1 || due[0].Lane != second.Author.Scope() || due[0].Kind != LookupAttempt {
		t.Fatal("uncertain resend skipped receipt recovery", due, err)
	}
}
