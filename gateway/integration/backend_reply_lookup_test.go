package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendReplyRootIntegrityRetainedLookup(t *testing.T) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	if err := author.SendWithID("#gateway-fixture", "lookup parent", "lookup-parent"); err != nil {
		t.Fatal(err)
	}
	parent := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "lookup-parent" })
	proxy, done, _, replies := backendProxy(t, socket, "wrong-root", nil)
	c, identity := acceptanceJournalFixture(t, proxy, token)
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := core.Request{Version: 1, RequestID: "lookup-root", Operation: core.Reply, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Message: "lookup reply"}
	if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Seed actual acceptance, then lose the process-owned result before saving it.
	if err := author.ReplyWithID(parent.ID, r.Message, a.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == a.Job.UpstreamID })
	if receipt.ThreadID != parent.ID {
		t.Fatal("native reply root mismatch")
	}
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
	o, err := b.Run(ctx, g)
	if err != nil || o.Receipt != nil || o.Completion.Failure.Code != core.DeliveryUncertain || o.Completion.MayHavePosted {
		t.Fatal("corrupted retained reply root established acceptance", o, err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lookup lease leaked")
	}
	if replies.Load() != 0 {
		t.Fatal("receipt recovery reposted a reply")
	}
	j, err := s.Complete(ctx, g, o.Completion, time.Now())
	if err != nil || !j.Result.AcceptanceUncertain || j.Result.State == core.Accepted {
		t.Fatal("lookup lost prior acceptance uncertainty", j, err)
	}
}
