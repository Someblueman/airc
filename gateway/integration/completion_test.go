package gateway_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// This drives the primitives explicitly; it does not claim an automatic worker.
func TestWireUnknownReceiptCompletionThenSend(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	c, identity := acceptanceJournalFixture(t, socket, token)
	ctx := context.Background()
	s, err := store.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "logical-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "send after a known pre-write failure and unknown lookup"}
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
	// A canceled dial never produces a registered lease or a message write.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	client, err := irc.DialContext(canceled, irc.Config{Network: "unix", Addr: socket, Nick: "fixture-author", IdentityToken: token, Ephemeral: true})
	if client != nil {
		_ = client.Close()
		t.Fatal("canceled dial returned a lease")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("unexpected dial failure", err)
	}
	j, err := s.Complete(ctx, g, store.Completion{Failure: core.Failure{Code: core.UpstreamUnavailable, Transient: true}}, time.Now())
	if err != nil || j.Result.Accepted == nil || *j.Result.Accepted || j.Result.AcceptanceUncertain {
		t.Fatal("pre-write failure misrepresented", j, err)
	}
	timer := time.NewTimer(time.Until(time.Unix(*j.Result.NextAttemptAt, 0)))
	defer timer.Stop()
	<-timer.C
	lg, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.LookupAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	la, err := lg.Consume(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := author.RetryRequest(la.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	unknown := next(t, author, func(e *irc.RawEvent) bool { return e.Command == "488" })
	code, err := strconv.Atoi(unknown.Command)
	if err != nil {
		t.Fatal(err)
	}
	j, err = s.Complete(ctx, lg, store.Completion{Failure: core.Failure{Code: core.DeliveryUncertain, Transient: true, UpstreamCode: code}}, time.Now())
	if err != nil || j.Result.State != core.RetryWait || j.Result.NextAction != "send" || j.Result.AcceptanceUncertain {
		t.Fatal("unknown lookup changed known no-write evidence", j, err)
	}
	g, err = s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	again, err := g.Consume(time.Now())
	if err != nil || again.Job.UpstreamID != a.Job.UpstreamID || again.Job.Result.DuplicateRisk || again.Job.Result.Accepted != nil {
		t.Fatal("retry identity/facts changed", again, err)
	}
	if err := author.SendWithID(again.Job.Request.Target.Name, again.Job.Request.Message, again.Job.UpstreamID); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == again.Job.UpstreamID })
	observedAt := time.Now()
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if seen.From != "fixture-author" || seen.Message != r.Message || seen.Target != r.Target.Name {
		t.Fatal("observer mismatch")
	}
	j, err = s.RecordAcceptance(ctx, g, receipt, observedAt, time.Now())
	if err != nil || j.Result.State != core.Accepted || j.Result.SendAttempts != 2 || j.Result.LookupAttempts != 1 || j.Result.Failure != nil {
		t.Fatal("acceptance did not resolve completion", j, err)
	}
}
