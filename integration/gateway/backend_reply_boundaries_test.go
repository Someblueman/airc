package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendReplyRootIntegrityParentDeadlineAndPreRevocationGrant(t *testing.T) {
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
	if err := author.SendWithID("#gateway-fixture", "boundary parent", "boundary-parent"); err != nil {
		t.Fatal(err)
	}
	parent := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "boundary-parent" })
	for _, mode := range []string{"wrong-root", "withhold-parent", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			proxy, done, _, replies := backendProxy(t, socket, mode, nil)
			c, identity := acceptanceJournalFixture(t, proxy, token)
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			s, err := store.Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := core.Request{Version: 1, RequestID: "reply-boundary-" + mode, Operation: core.Reply, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID, Message: mode + " body"}
			if mode == "withhold-parent" {
				expires := time.Now().Unix() + 2
				r.ExpiresAt = &expires
			}
			if _, _, err := s.Admit(ctx, identity, r, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, identity.ID(), r.RequestID, store.SendAttempt, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "revoked" {
				if _, err := s.Revoke(ctx, identity.ID()); err != nil {
					t.Fatal(err)
				}
			}
			o, err := b.Run(ctx, g)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reply boundary leaked a lease")
			}
			if mode == "withhold-parent" {
				if o.Receipt != nil || o.Completion.MayHavePosted || !o.Completion.Failure.Transient || replies.Load() != 0 {
					t.Fatal("parent timeout wrote a reply or claimed acceptance", o)
				}
			} else {
				if replies.Load() != 1 {
					t.Fatal("bounded grant did not perform exactly one reply")
				}
				seen := next(t, observer, func(e *irc.MessageEvent) bool {
					return e.RequestID == o.Attempt.Job.UpstreamID && e.Message == r.Message
				})
				if seen.ReplyTo != parent.ID || seen.ThreadID != parent.ID {
					t.Fatal("server reply linkage mismatch")
				}
				if mode == "wrong-root" && (o.Receipt != nil || !o.Completion.MayHavePosted || o.Completion.Failure.Code != core.DeliveryUncertain) {
					t.Fatal("receipt root was not checked against retained parent", o)
				}
			}
			if mode == "revoked" {
				if o.Receipt == nil {
					t.Fatal("permitted pre-revocation grant discarded", o)
				}
				if _, err := s.RecordAcceptance(ctx, g, o.Receipt, o.ObservedAt, time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err := b.Run(ctx, g); !errors.Is(err, store.ErrNotReady) {
					t.Fatal("revoked exception reused", err)
				}
			} else {
				j, err := s.Complete(ctx, g, o.Completion, time.Now())
				if err != nil || j.Result.AcceptanceUncertain != (mode == "wrong-root") {
					t.Fatal("completion lost reply uncertainty", j, err)
				}
			}
		})
	}
}
