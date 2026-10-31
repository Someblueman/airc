package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendSendAndRetainedLookup(t *testing.T) {
	t.Parallel()
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	c, identity := acceptanceJournalFixture(t, socket, token)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := core.Request{Version: 1, RequestID: "backend-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "bounded SDK backend\nindependent observer"}
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err := b.Run(ctx, g)
	if err != nil || o.Receipt == nil {
		t.Fatal("backend failed to observe acceptance", o, err)
	}
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID })
	if seen.From != "fixture-author" || seen.Target != r.Target.Name || seen.Message != r.Message {
		t.Fatal("observer mismatch")
	}
	if _, err := b.Run(ctx, g); !errors.Is(err, store.ErrNotReady) {
		t.Fatal("backend reused consumed grant", err)
	}
	// Lose the result before saving, then recover on another short-lived lease.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	// A restart forgets the token: resubmitting it resumes the job.
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err = s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.LookupAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	retained, err := b.Run(ctx, g)
	if err != nil || retained.Receipt == nil || retained.Receipt.ID != o.Receipt.ID {
		t.Fatal("retained lookup failed", retained, err)
	}
	j, err := s.RecordAcceptance(ctx, g, retained.Receipt, retained.ObservedAt, time.Now())
	if err != nil || j.Result.State != core.Accepted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 1 {
		t.Fatal("backend evidence was not saved", j, err)
	}
	if _, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now()); !errors.Is(err, store.ErrNotReady) {
		t.Fatal("saved acceptance resent", err)
	}
}
