package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBackendReactionParentFeaturesAndLostReceipt(t *testing.T) {
	t.Parallel()
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", token, true, true)
	_ = provision.Close()
	author := dial(t, socket, "fixture-author", token, false, true)
	parents := map[string]string{}
	for _, target := range []string{"#gateway-fixture", "#foreign-room", "fixture-peer"} {
		if err := author.SendWithID(target, "reaction boundary parent", strings.TrimPrefix(target, "#")); err != nil {
			t.Fatal(err)
		}
		parents[target] = next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Target == target }).ID
	}
	for _, test := range []struct {
		name, mode, parent, reaction string
		code                         core.ErrorCode
		writes                       int64
	}{
		{"foreign", "relay", parents["#foreign-room"], "seen", core.Forbidden, 0},
		{"direct", "relay", parents["fixture-peer"], "seen", core.Forbidden, 0},
		{"missing", "relay", strings.Repeat("f", 32), "seen", core.InvalidRequest, 0},
		{"older-daemon", "level-zero", parents["#gateway-fixture"], "seen", core.UnsupportedOperation, 0},
		{"parent-timeout", "withhold-parent", parents["#gateway-fixture"], "seen", core.UpstreamUnavailable, 0},
		{"wrong-root", "wrong-root", parents["#gateway-fixture"], "agree", core.DeliveryUncertain, 1},
		{"lost-receipt", "withhold", parents["#gateway-fixture"], "checking", core.DeliveryUncertain, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy, done, _, writes := backendProxy(t, socket, test.mode, nil)
			c, _ := acceptanceJournalFixture(t, proxy, token)
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
			r := core.Request{Version: 1, RequestID: "reaction-" + test.name, Operation: core.ReactionAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: test.parent, Reaction: test.reaction}
			if _, _, err := s.AdmitAuthor(context.Background(), identity, r, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(context.Background(), journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			o, err := b.Run(ctx, g)
			cancel()
			if err != nil || o.Receipt != nil || o.Completion.Failure.Code != test.code || o.Completion.MayHavePosted != (test.writes > 0) {
				t.Fatal("reaction boundary outcome mismatch", o, err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reaction lease survived outcome")
			}
			if writes.Load() != test.writes {
				t.Fatal("unpermitted reaction write", writes.Load(), test.writes)
			}
			j, err := s.Complete(context.Background(), g, o.Completion, time.Now())
			if err != nil || j.Result.AcceptanceUncertain != (test.writes > 0) {
				t.Fatal("reaction completion lost uncertainty", j, err)
			}
		})
	}
}
