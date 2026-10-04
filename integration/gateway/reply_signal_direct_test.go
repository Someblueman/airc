//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReplySignalDirectIncomingOutgoingAndForeignParent(t *testing.T) {
	peerTarget := core.Target{Kind: "direct", Name: "signal-peer"}
	selfTarget := core.Target{Kind: "direct", Name: "fixture-author"}
	f := newReactionTransportFixtureWithTargets(t, false, []core.Target{peerTarget, selfTarget})
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	peer := newReplySignalObserver(t, f.socket, peerTarget)
	outgoing := seedPinMutationMessage(t, f.socket, peerTarget.Name, "signal-direct-outgoing")
	if err := peer.SendWithID("FIXTURE-AUTHOR", "incoming question", "signal-direct-incoming"); err != nil {
		t.Fatal(err)
	}
	incoming := next(t, peer, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "signal-direct-incoming" }).ID
	for _, parent := range []struct{ name, id, nativeTarget string }{{"outgoing", outgoing, peerTarget.Name}, {"incoming", incoming, "FIXTURE-AUTHOR"}} {
		for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
			r := replySignalRequest("signal-direct-"+parent.name+"-"+map[core.Operation]string{core.ReplySignalSet: "set", core.ReplySignalCancel: "cancel"}[op], parent.id, op, &peerTarget)
			roomBearerPost(t, f, r, 1)
			result := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
			assertReplySignalFacts(t, result, r, nextReplySignal(t, peer, parent.id))
			if result.ReplySignal.NativeTarget != parent.nativeTarget || result.ReplySignal.Target != peerTarget {
				t.Fatalf("direct participant direction lost: %+v", result.ReplySignal)
			}
		}
	}
	selfObserver := dial(t, f.socket, "fixture-author", fixtureAccount, false, false)
	defer selfObserver.Close()
	selfParent := seedPinMutationMessage(t, f.socket, selfTarget.Name, "signal-self-parent")
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		r := replySignalRequest("signal-self-"+map[core.Operation]string{core.ReplySignalSet: "set", core.ReplySignalCancel: "cancel"}[op], selfParent, op, &selfTarget)
		roomBearerPost(t, f, r, 1)
		assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, selfObserver, selfParent))
	}
	foreignTarget := core.Target{Kind: "direct", Name: "signal-outsider"}
	foreignObserver := newReplySignalObserver(t, f.socket, foreignTarget)
	foreignSender := dial(t, f.socket, "signal-alien", "", false, false)
	defer foreignSender.Close()
	if err := foreignSender.SendWithID(foreignTarget.Name, "foreign DM", "signal-foreign-parent"); err != nil {
		t.Fatal(err)
	}
	foreign := next(t, foreignSender, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "signal-foreign-parent" }).ID
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		r := replySignalRequest("signal-foreign-"+map[core.Operation]string{core.ReplySignalSet: "set", core.ReplySignalCancel: "cancel"}[op], foreign, op, &peerTarget)
		roomBearerPost(t, f, r, 1)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.ReplySignal != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
			t.Fatalf("foreign direct proof was not blocked: %+v", blocked)
		}
		assertNoReplySignal(t, foreignObserver)
	}
}
