//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMessageMutationForeignRoomAndPinnedEviction(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	foreignTarget := core.Target{Kind: "room", Name: "#post-private"}
	foreignObserver := newReplySignalObserver(t, f.socket, foreignTarget)
	foreign := seedPinMutationMessage(t, f.socket, foreignTarget.Name, "post-foreign-parent")
	for _, op := range []core.Operation{core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-forged-"+strings.TrimPrefix(string(op), "message."), op, target, foreign)
		roomBearerPost(t, f, r, 1)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.AcceptanceUncertain || blocked.Accepted == nil || *blocked.Accepted || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
			t.Fatalf("foreign parent write not blocked: %+v", blocked)
		}
		assertNoMessageMutation(t, foreignObserver)
	}
	parent := seedPinMutationMessage(t, f.socket, target.Name, "post-pinned-parent")
	seedNativePin(t, f, parent)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for i := range 33 {
		id := fmt.Sprintf("post-evict-%d", i)
		if err := author.SendWithID(target.Name, id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	_ = author.Close()
	if snapshot := topicHistorySnapshot(t, f.socket); len(snapshot) != 32 || snapshot[parent] != "" {
		t.Fatal("history eviction failed", snapshot)
	}
	observer := newReplySignalObserver(t, f.socket, *target)
	for _, op := range []core.Operation{core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-pinned-"+strings.TrimPrefix(string(op), "message."), op, target, parent)
		roomBearerPost(t, f, r, 1)
		result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
		message := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		assertMessageMutationFacts(t, result, r, message)
		pins := queryPinMutationNative(t, f.socket, target.Name)
		if len(pins) != 1 || pins[0].ID != parent || pins[0].SupersededBy != result.MessageID || pins[0].Retracted != (op == core.MessageRetract) {
			t.Fatalf("pinned original annotation: %+v", pins)
		}
	}
}

func TestMessageMutationDirectOutgoingAndNonAuthor(t *testing.T) {
	peerTarget := core.Target{Kind: "direct", Name: "post-peer"}
	f := newReactionTransportFixtureWithTargets(t, false, []core.Target{peerTarget})
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	peer := newReplySignalObserver(t, f.socket, peerTarget)
	parent := ""
	for _, op := range []core.Operation{core.MessageAction, core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-direct-"+strings.TrimPrefix(string(op), "message."), op, &peerTarget, parent)
		roomBearerPost(t, f, r, 1)
		result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
		message := next(t, peer, func(e *irc.MessageEvent) bool { return e.ID == result.MessageID })
		assertMessageMutationFacts(t, result, r, message)
		if result.RecipientConnected == nil || !*result.RecipientConnected {
			t.Fatal("online direct recipient fact lost", result)
		}
		parent = result.MessageID
	}
	if err := peer.SendWithID("fixture-author", "incoming authored by peer", "post-incoming"); err != nil {
		t.Fatal(err)
	}
	incoming := next(t, peer, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "post-incoming" }).ID
	for _, op := range []core.Operation{core.MessageCorrect, core.MessageRetract} {
		r := messageMutationRequest("post-nonauthor-"+strings.TrimPrefix(string(op), "message."), op, &peerTarget, incoming)
		roomBearerPost(t, f, r, 1)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.AcceptanceUncertain || blocked.Accepted == nil || *blocked.Accepted || blocked.Failure == nil {
			t.Fatalf("non-author correction accepted: %+v", blocked)
		}
		assertNoMessageMutation(t, peer)
	}
}
