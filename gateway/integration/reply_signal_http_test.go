//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

// TestReplySignalHTTPThrottleAndCancel maps reply_signal.set and
// reply_signal.cancel to the native waiting signal: the saved ACK equals the
// broadcast and the native waiting state, and a set inside the native
// two-second throttle saves the prior preparation without broadcasting.
func TestReplySignalHTTPThrottleAndCancel(t *testing.T) {
	f := newReactionHTTPFixture(t)
	target := contractRoom()
	observer := newReplySignalObserver(t, f.socket, *target)
	parent := seedPinMutationMessage(t, f.socket, target.Name, "reply-signal-http-parent")
	r := replySignalRequest("reply-signal-http", parent, core.ReplySignalSet, target)
	headerPost(t, f, r)
	first := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
	assertReplySignalFacts(t, first, r, nextReplySignal(t, observer, parent))
	if waiting := queryNativeWaitingSignal(t, f.socket, parent); len(waiting) != 1 || waiting[0].ID != parent || waiting[0].Action != first.ReplySignal.Action || waiting[0].Text != first.ReplySignal.Note || !waiting[0].ExpiresAt.Equal(first.ReplySignal.ExpiresAt) {
		t.Fatalf("native waiting differs from saved preparation: %+v ack=%+v", waiting, first.ReplySignal)
	}
	r.RequestID = "reply-signal-http-throttle"
	r.ReplySignal = &core.ReplySignalInput{Note: "different note", TTLSeconds: 1}
	headerPost(t, f, r)
	throttled := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
	assertReplySignalFacts(t, throttled, r, nil)
	if *throttled.ReplySignal != *first.ReplySignal {
		t.Fatalf("native prior preparation not preserved: first=%+v second=%+v", first.ReplySignal, throttled.ReplySignal)
	}
	assertNoReplySignal(t, observer)
	r = replySignalRequest("reply-signal-http-cancel", parent, core.ReplySignalCancel, target)
	if status, admission := reactionHTTPCall(t, f, f.cred.token(t, r, true)); status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil || !admission.Result.Durable {
		t.Fatalf("request-route cancel admission: %d %+v", status, admission)
	}
	assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
	if waiting := queryNativeWaitingSignal(t, f.socket, parent); len(waiting) != 0 {
		t.Fatalf("native cancellation left waiting state: %+v", waiting)
	}
}
