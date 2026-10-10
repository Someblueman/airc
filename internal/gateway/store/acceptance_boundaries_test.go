//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
	"testing"
	"time"
)

func TestReceiptCorrelationAndFencing(t *testing.T) {
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
	g, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	changes := []func(*irc.SendReceiptEvent){func(r *irc.SendReceiptEvent) { r.RequestID = "different" }, func(r *irc.SendReceiptEvent) { r.Target = "#Denied" }, func(r *irc.SendReceiptEvent) { r.From = "stranger" }, func(r *irc.SendReceiptEvent) { r.Message = "different" }, func(r *irc.SendReceiptEvent) { r.Receipt = nil }, func(r *irc.SendReceiptEvent) { r.Receipt.Accepted = false }, func(r *irc.SendReceiptEvent) { r.ReplyTo = strings.Repeat("b", 32) }}
	for _, change := range changes {
		r := receiptFor(a)
		change(r)
		if _, err := s.RecordAcceptance(ctx, g, r, now, now); !errors.Is(err, ErrInvalid) {
			t.Fatal("uncorrelated receipt saved", err)
		}
	}
	// The caller's consumed snapshot must not alter the private matching intent.
	a.Job.Request.Target.Name = "#Denied"
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("mutable consumed target changed correlation", err)
	}
	a.Job.Request.Target.Name = "#Allowed"
	if _, err := s.db.ExecContext(ctx, "UPDATE jobs SET attempt_id='newer-attempt'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now); !errors.Is(err, ErrStaleAttempt) {
		t.Fatal("old completion overwrote newer marker", err)
	}
}

func TestLargeEscapedValidReceipt(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := request()
	r.Message = strings.Repeat("<", 4096)
	if _, _, err := s.Admit(ctx, identity, r, 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, identity.ID(), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAcceptance(ctx, g, receiptFor(a), now, now); err != nil {
		t.Fatal("JSON escaping rejected a valid full-size message", err)
	}
}
