package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWireReceiptBecomesDurableAcceptance(t *testing.T) {
	t.Parallel()
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const accountToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", accountToken, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", accountToken, false, true)
	c, identity := acceptanceJournalFixture(t, socket, accountToken)
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := core.Request{Version: 1, RequestID: "logical-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "journal to wire\r\nindependent observer"}
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
	if err := author.SendWithID(a.Job.Request.Target.Name, a.Job.Request.Message, a.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == a.Job.UpstreamID })
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if seen.From != "fixture-author" || seen.Target != r.Target.Name || seen.Message != a.Job.Request.Message {
		t.Fatal("observer disagrees with admitted operation")
	}
	// Simulate losing the receipt before saving. Recover it through public RETRY,
	// with a separate lookup grant and without another send.
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
	la, err := g.Consume(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := author.RetryRequest(la.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	retained := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == la.Job.UpstreamID })
	observedAt := time.Now()
	if retained.ID != receipt.ID {
		t.Fatal("lookup returned a different message")
	}
	receipt = retained
	j, err := s.RecordAcceptance(ctx, g, receipt, observedAt, time.Now())
	if err != nil || j.Result.State != core.Accepted || j.Result.Persisted == nil || !*j.Result.Persisted || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 1 {
		t.Fatal("wire receipt was not saved", err)
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
	j, new, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now())
	if err != nil || new || j.Receipt == nil || j.Receipt.ID != receipt.ID {
		t.Fatal("lost response/reopen failed to replay acceptance", err)
	}
	if _, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now()); !errors.Is(err, store.ErrNotReady) {
		t.Fatal("saved real acceptance allowed another send", err)
	}
	reply := core.Request{Version: 1, RequestID: "logical-reply", Operation: core.Reply, Target: r.Target, Message: "reply through journal", ParentID: receipt.ID}
	if _, _, err := s.AdmitAuthor(ctx, identity, reply, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	rg, err := s.Reserve(ctx, journalScope(t, s, identity), reply.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ra, err := rg.Consume(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := author.ReplyWithID(reply.ParentID, ra.Job.Request.Message, ra.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	rr := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == ra.Job.UpstreamID })
	observedReplyAt := time.Now()
	ro := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == rr.ID })
	if ro.ReplyTo != receipt.ID || ro.ThreadID != receipt.ID {
		t.Fatal("observer reply linkage lost")
	}
	rj, err := s.RecordAcceptance(ctx, rg, rr, observedReplyAt, time.Now())
	if err != nil || rj.Receipt.ReplyTo != receipt.ID || rj.Receipt.ThreadID != receipt.ID {
		t.Fatal("pre-revocation reply acceptance lost", err)
	}
}
