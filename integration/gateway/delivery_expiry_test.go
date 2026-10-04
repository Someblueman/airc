package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDeliveryExpiresIdleWorkWhileReceiptLeaseIsBusy(t *testing.T) {
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
	c.Limits.Pending, c.Limits.PendingPerPrincipal = 2, 2
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
	r := core.Request{Version: 1, RequestID: "busy", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "busy actual post"}
	j, _, err := s.Admit(ctx, identity, r, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		t.Fatal(err)
	}
	service, stop := context.WithCancel(ctx)
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
			t.Error("expiry runner did not join")
		}
		select {
		case <-leaseDone:
		case <-time.After(time.Second):
			t.Error("expiry runner leaked relay")
		}
	})
	next(t, observer, func(e *irc.MessageEvent) bool { return e.RequestID == j.UpstreamID })
	r.RequestID, r.Message = "idle-expiry", "must not post"
	expires := time.Now().Unix() + 2
	r.ExpiresAt = &expires
	if _, _, err := s.Admit(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	third := r
	third.RequestID, third.ExpiresAt = "after-expiry", nil
	if _, _, err := s.Admit(ctx, identity, third, 0, time.Now()); !errors.Is(err, store.ErrCapacity) {
		t.Fatal("pending quota not enforced", err)
	}
	j = awaitDelivery(t, s, identity, r.RequestID, func(j store.Job) bool { return j.Result.State == core.Expired })
	if j.Result.SendAttempts != 0 || j.Result.Accepted == nil || *j.Result.Accepted || j.Result.AcceptanceUncertain {
		t.Fatal("idle expiry claimed a write or lost evidence", j)
	}
	if _, _, err := s.Admit(ctx, identity, third, 0, time.Now()); err != nil {
		t.Fatal("automatic expiry failed to release quota", err)
	}
}
