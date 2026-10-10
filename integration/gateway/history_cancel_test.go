package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReadBackendCancellationJoinsWithheldSnapshotDespiteLiveNoise(t *testing.T) {
	for _, op := range []core.Operation{core.HistoryRead, core.Search, core.Watch} {
		t.Run(string(op), func(t *testing.T) { readCancellation(t, op) })
	}
}

func readCancellation(t *testing.T, op core.Operation) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	mode := "withhold-history"
	if op == core.Search {
		mode = "withhold-search"
	}
	proxy, joined, _, checks := backendProxy(t, socket, mode, nil)
	c, _ := acceptanceJournalFixture(t, proxy, token)
	c.Principals[0].Operations = append(c.Principals[0].Operations, op)
	identity := fixtureIdentity(t, c)
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{Version: 1, RequestID: "cancel-history", Operation: op, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
	if op == core.Search {
		r.Query = "x"
	}
	g, err := s.AuthorizeRead(context.Background(), identity, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := b.Read(ctx, g, nil); done <- err; close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("history survived cancellation")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for checks.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("history never issued snapshot command")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, backend.ErrReadUnavailable) {
			t.Fatal("cancelled read returned success", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled history did not return")
	}
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("history lease survived return")
	}
	if checks.Load() != 1 {
		t.Fatal("history reconnected/retried", checks.Load())
	}
	if _, err := s.Lookup(context.Background(), identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("read created a delivery job", err)
	}
}

func TestReadBackendByteOverflowReturnsNoPartialPageAndJoins(t *testing.T) {
	for _, op := range []core.Operation{core.HistoryRead, core.Search, core.Watch} {
		t.Run(string(op), func(t *testing.T) { readOverflow(t, op) })
	}
}

func readOverflow(t *testing.T, op core.Operation) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, socket, "fixture-author", token, true, true)
	for i := range 16 {
		id := fmt.Sprintf("large-history-%d", i)
		if err := author.SendWithID("#gateway-fixture", strings.Repeat("x", 4096), id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	proxy, joined, _, _ := backendProxy(t, socket, "pass", nil)
	c, _ := acceptanceJournalFixture(t, proxy, token)
	c.Principals[0].Operations = append(c.Principals[0].Operations, op)
	identity := fixtureIdentity(t, c)
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{Version: 1, RequestID: "large-history", Operation: op, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 16}
	if op == core.Search {
		r.Query = "x"
	}
	g, err := s.AuthorizeRead(context.Background(), identity, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	page, err := b.Read(context.Background(), g, nil)
	if !errors.Is(err, store.ErrCapacity) || len(page.Messages) != 0 || page.Cursor != "" {
		t.Fatal("oversized snapshot returned partial success", page, err)
	}
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("overflow lease survived return")
	}
}
