package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/delivery"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDeliveryRecoversLostAcceptanceAfterReopen(t *testing.T) {
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
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := core.Request{Version: 1, RequestID: "automatic-recovery", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "lost acceptance"}
	if _, _, err := s.AdmitAuthor(context.Background(), identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(context.Background(), journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err := b.Run(context.Background(), g)
	if err != nil || o.Receipt == nil {
		t.Fatal("native acceptance failed", o, err)
	}
	next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID })
	// Neither acceptance nor completion is saved; reopening must recover the
	// marker without resetting the send budget or issuing an immediate resend.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	// A registered author's token lives only in memory: resubmitting
	// the request resumes the job the reopen parked.
	if _, _, err := s.AdmitAuthor(context.Background(), identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("recovery workers failed to join")
		}
	})
	j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
	if j.Result.MessageID != o.Receipt.ID || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 1 {
		t.Fatal("automatic recovery reposted or reset counters", j)
	}
}
