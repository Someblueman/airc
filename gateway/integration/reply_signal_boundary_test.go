//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestReplySignalForeignRoomAndRetainedNoOpCancel(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	foreignTarget := core.Target{Kind: "room", Name: "#signal-foreign"}
	foreignObserver := newReplySignalObserver(t, f.socket, foreignTarget)
	foreign := seedPinMutationMessage(t, f.socket, foreignTarget.Name, "signal-foreign-room")
	setReplySignalNative(t, f.socket, foreign, &core.ReplySignalInput{Note: "foreign unchanged", TTLSeconds: 120})
	nextReplySignal(t, foreignObserver, foreign)
	before := queryNativeWaitingSignal(t, f.socket, foreign)
	if len(before) != 1 {
		t.Fatal("native foreign positive control missing", before)
	}
	for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
		r := replySignalRequest("signal-forged-"+strings.ReplaceAll(string(op), ".", "-"), foreign, op, target)
		headerPost(t, f, r)
		blocked := waitHTTPPinResult(t, f, r.RequestID, core.Blocked)
		if blocked.ReplySignal != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden {
			t.Fatalf("foreign room proof allowed write: %+v", blocked)
		}
		if after := queryNativeWaitingSignal(t, f.socket, foreign); !reflect.DeepEqual(before, after) {
			t.Fatalf("forged signal changed native state: before=%+v after=%+v", before, after)
		}
		assertNoReplySignal(t, foreignObserver)
	}
	// A cancel of an already cancelled preparation is a retained native no-op
	// that still saves and broadcasts its acknowledgement.
	parent := seedPinMutationMessage(t, f.socket, target.Name, "signal-noop-parent")
	observer := newReplySignalObserver(t, f.socket, *target)
	r := replySignalRequest("signal-before-cancel", parent, core.ReplySignalSet, target)
	headerPost(t, f, r)
	assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
	r = replySignalRequest("", parent, core.ReplySignalCancel, target)
	for _, id := range []string{"signal-cancel", "signal-retained-noop-cancel"} {
		r.RequestID = id
		headerPost(t, f, r)
		assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
		if entries := queryNativeWaitingSignal(t, f.socket, parent); len(entries) != 0 {
			t.Fatal("native cancellation did not clear signal", entries)
		}
	}
}
