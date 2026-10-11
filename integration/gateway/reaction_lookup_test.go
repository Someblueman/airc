package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReactionLookupNeverOpensNativeSession(t *testing.T) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	if err := author.SendWithID("#gateway-fixture", "lookup parent", "lookup-parent"); err != nil {
		t.Fatal(err)
	}
	parent := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "lookup-parent" })
	proxy, _, connected, writes := backendProxy(t, "", "stall", nil)
	c, _ := acceptanceJournalFixture(t, proxy, token)
	identity := fixtureIdentity(t, c)
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	policy := core.ReceiptOnly
	r := core.Request{Version: 1, RequestID: "reaction-lookup", Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Reaction: "seen", DeliveryPolicy: &policy}
	ctx := context.Background()
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Consume(time.Now()); err != nil {
		t.Fatal(err)
	}
	// Seed the exact native reaction, then lose the process-owned result. The
	// frozen backend address is an instrumented listener for recovery only.
	if err := author.React(parent.ID, r.Reaction); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.SendReceiptEvent) bool { return e.ReplyTo == parent.ID && e.Reaction == r.Reaction })
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
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
	o, err := b.Run(lookupCtx, g)
	cancel()
	if err != nil || o.Receipt != nil || o.Completion.Failure.Code != core.DeliveryUncertain || o.Completion.MayHavePosted {
		t.Fatal("lookup invented acceptance or a write", o, err)
	}
	select {
	case <-connected:
		t.Fatal("reaction lookup opened a native session")
	default:
	}
	if writes.Load() != 0 {
		t.Fatal("reaction lookup issued a command")
	}
	if _, err := s.Complete(ctx, g, o.Completion, time.Now()); err != nil {
		t.Fatal(err)
	}
}
