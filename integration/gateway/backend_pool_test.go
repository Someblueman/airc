package gateway_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// runSend admits and delivers one send through b, saving its outcome.
func runSend(t *testing.T, s *store.Store, b *backend.Backend, sub store.Submission, id string) backend.Outcome {
	t.Helper()
	ctx := context.Background()
	r := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "pooled " + id}
	if _, _, err := s.AdmitAuthor(ctx, sub, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, sub.Author.Scope(), id, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err := b.Run(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if o.Acceptance != (core.Acceptance{}) {
		_, err = s.Accept(ctx, g, o.Acceptance, o.ObservedAt, time.Now())
	} else {
		_, err = s.Complete(ctx, g, o.Completion, time.Now())
	}
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// TestBackendReusesAuthorSession sends three jobs for one author through a
// proxy that accepts a single connection: each is accepted, so all three
// ran on the session the first one logged in.
func TestBackendReusesAuthorSession(t *testing.T) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	proxy, done, _, _ := backendProxy(t, socket, "relay", nil)
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
	for i := range 3 {
		if o := runSend(t, s, b, identity, "pooled-"+strconv.Itoa(i)); o.Receipt == nil {
			t.Fatal("send on a reused session was not accepted", i, o.Completion)
		}
	}
	select {
	case <-done:
		t.Fatal("the kept session closed while idle")
	default:
	}
	b.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close left the kept session open")
	}
}

// TestBackendGuestReuseYieldsNickname keeps a guest's session, then a
// connected client takes the nickname: the next send must not reuse the
// session to speak as it, and fails as a fresh guest login would.
func TestBackendGuestReuseYieldsNickname(t *testing.T) {
	socket := fixture(t)
	c, _ := acceptanceJournalFixture(t, socket, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	guest := store.Submission{Author: core.Author{Nick: "pool-guest"}}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if o := runSend(t, s, b, guest, "guest-first"); o.Receipt == nil {
		t.Fatal("guest send failed", o.Completion)
	}
	holder := dial(t, socket, "pool-guest", "", false, false)
	defer holder.Close()
	o := runSend(t, s, b, guest, "guest-second")
	if o.Receipt != nil || o.Completion.Failure.Code != core.NickInUse {
		t.Fatal("a kept guest session spoke as a held nickname", o.Receipt, o.Completion)
	}
}
