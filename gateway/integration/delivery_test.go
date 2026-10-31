package gateway_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/delivery"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func awaitDelivery(t *testing.T, s *store.Store, identity store.Submission, id string, predicate func(store.Job) bool) store.Job {
	t.Helper()
	deadline := time.After(8 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		j, err := s.LookupAuthor(context.Background(), identity, id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(j) {
			return j
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("delivery did not reach required state", j)
		}
	}
}

func TestDeliveryResumesOutageWithoutCaller(t *testing.T) {
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
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		t.Fatal(err)
	}
	// Hide only this disposable fixture's socket pathname; its existing observer
	// remains connected. No daemon/account restart is involved.
	if err := os.Rename(socket, socket+".offline"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(socket+".offline", socket) })
	caller, closeCaller := context.WithCancel(context.Background())
	r := core.Request{Version: 1, RequestID: "automatic-outage", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "durable caller gone"}
	if _, _, err := s.AdmitAuthor(caller, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	closeCaller()
	service, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(service) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("delivery workers failed to join")
		}
	})
	awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.RetryWait && j.Result.SendAttempts > 0 })
	if err := os.Rename(socket+".offline", socket); err != nil {
		t.Fatal(err)
	}
	j := awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
	seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == j.Result.MessageID })
	if seen.From != "fixture-author" || seen.Target != r.Target.Name || seen.Message != r.Message || j.Result.SendAttempts < 2 || j.Result.DuplicateRisk {
		t.Fatal("automatic delivery evidence mismatch", seen, j)
	}
}
