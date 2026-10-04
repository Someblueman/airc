package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func replyAttempt(t *testing.T, s *store.Store, b *backend.Backend, identity auth.Identity, r core.Request) (*store.Grant, backend.Outcome) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := s.Admit(ctx, identity, r, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err := b.Run(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	return g, o
}

func TestBackendAuthorizedRoomAndNestedReplies(t *testing.T) {
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
	if err := author.SendWithID("#gateway-fixture", "retained root", "root"); err != nil {
		t.Fatal(err)
	}
	root := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "root" })
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
	parent := root.ID
	for _, id := range []string{"reply", "nested"} {
		r := core.Request{Version: 1, RequestID: id, Operation: core.Reply, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent, Message: id + " body"}
		g, o := replyAttempt(t, s, b, identity, r)
		if o.Receipt == nil || o.Receipt.ReplyTo != parent || o.Receipt.ThreadID != root.ID {
			t.Fatal("parent-authorized reply failed", o)
		}
		seen := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID })
		if seen.From != "fixture-author" || seen.Target != r.Target.Name || seen.ReplyTo != parent || seen.ThreadID != root.ID || seen.Message != r.Message {
			t.Fatal("observer reply mismatch", seen)
		}
		if id == "reply" {
			// A lost reply result recovers the full parent/root receipt on a fresh lease.
			original := o.Receipt.ID
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = store.Open(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			g, err = s.Reserve(context.Background(), identity.ID(), r.RequestID, store.LookupAttempt, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			o, err = b.Run(context.Background(), g)
			if err != nil || o.Receipt == nil || o.Receipt.ID != original || o.Receipt.ReplyTo != parent || o.Receipt.ThreadID != root.ID {
				t.Fatal("reply receipt lookup lost linkage", o, err)
			}
		}
		j, err := s.RecordAcceptance(context.Background(), g, o.Receipt, o.ObservedAt, time.Now())
		if err != nil || j.Result.State != core.Accepted {
			t.Fatal("reply acceptance not durable", j, err)
		}
		parent = o.Receipt.ID
	}
}

func TestBackendAuthorizedDirectRepliesBothDirections(t *testing.T) {
	socket := fixture(t)
	peer := dial(t, socket, "fixture-peer", "", false, false)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	c, _ := acceptanceJournalFixture(t, socket, token)
	// Recreate the verifier after provisioning an explicit DM destination.
	c.Principals[0].Targets = append(c.Principals[0].Targets, core.Target{Kind: "direct", Name: "fixture-peer"})
	identity := fixtureIdentity(t, c)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, direction := range []string{"outgoing", "incoming"} {
		sender, target := author, "fixture-peer"
		if direction == "incoming" {
			sender, target = peer, "fixture-author"
		}
		if err := sender.SendWithID(target, direction+" parent", direction); err != nil {
			t.Fatal(err)
		}
		parent := next(t, sender, func(e *irc.SendReceiptEvent) bool { return e.RequestID == direction })
		r := core.Request{Version: 1, RequestID: direction + "-reply", Operation: core.Reply, Target: &core.Target{Kind: "direct", Name: "fixture-peer"}, ParentID: parent.ID, Message: direction + " reply"}
		g, o := replyAttempt(t, s, b, identity, r)
		if o.Receipt == nil || o.Receipt.Target != "fixture-peer" || o.Receipt.ReplyTo != parent.ID {
			t.Fatal("DM participant mapping failed", o)
		}
		seen := next(t, peer, func(e *irc.MessageEvent) bool { return e.ID == o.Receipt.ID })
		if seen.From != "fixture-author" || seen.Target != "fixture-peer" || seen.ThreadID != parent.ID || seen.Message != r.Message {
			t.Fatal("DM reply observer mismatch", seen)
		}
		if _, err := s.RecordAcceptance(context.Background(), g, o.Receipt, o.ObservedAt, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}
