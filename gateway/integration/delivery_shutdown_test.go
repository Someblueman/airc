package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/delivery"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDeliveryShutdownJoinsLeaseAndSavesUncertainty(t *testing.T) {
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
	proxy, leaseDone, _, _ := backendProxy(t, socket, "withhold", nil)
	c, identity := acceptanceJournalFixture(t, proxy, token)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "shutdown", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "shutdown uncertain"}
	j, _, err := s.AdmitAuthor(context.Background(), identity, r, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	defer cancel()
	next(t, observer, func(e *irc.MessageEvent) bool { return e.RequestID == j.UpstreamID })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("shutdown did not join worker")
	}
	select {
	case <-leaseDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown leaked the socket relay")
	}
	j, err = s.LookupAuthor(context.Background(), identity, r.RequestID)
	if err != nil || !j.Result.AcceptanceUncertain || j.Result.Accepted != nil || j.Result.SendAttempts != 1 || j.Result.NextAction != "lookup" {
		t.Fatal("shutdown discarded possible acceptance", j, err)
	}
	if err := runner.Run(context.Background()); !errors.Is(err, store.ErrNotReady) {
		t.Fatal("runner reused lifetime", err)
	}
}
