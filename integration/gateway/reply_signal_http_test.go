//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestReplySignalHeadersHTTP1HTTP2AndNativeThrottle(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	parent := seedPinMutationMessage(t, f.socket, target.Name, "reply-signal-http-parent")
	r := replySignalRequest("reply-signal-http1", parent, core.ReplySignalSet, target)
	roomHeaderPost(t, f, r, 1)
	first := waitHTTPPinResult(t, f, r.RequestID, core.Accepted)
	assertReplySignalFacts(t, first, r, nextReplySignal(t, observer, parent))
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	r.RequestID = "reply-signal-http2-throttle"
	r.ReplySignal = &core.ReplySignalInput{Note: "different note", TTLSeconds: 1}
	roomHeaderPost(t, &h2, r, 2)
	throttled := waitHTTPPinResult(t, &h2, r.RequestID, core.Accepted)
	assertReplySignalFacts(t, throttled, r, nil)
	if *throttled.ReplySignal != *first.ReplySignal {
		t.Fatalf("native prior preparation not preserved: first=%+v second=%+v", first.ReplySignal, throttled.ReplySignal)
	}
	assertNoReplySignal(t, observer)
	r = replySignalRequest("reply-signal-http2-cancel", parent, core.ReplySignalCancel, target)
	roomHeaderPost(t, &h2, r, 2)
	assertReplySignalFacts(t, waitHTTPPinResult(t, &h2, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
	r = replySignalRequest("reply-signal-signed-http2", parent, core.ReplySignalSet, target)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	r.IssuedAt, r.ExpiresAt = &issued, &expires
	token := f.cred.token(t, r, false)
	status, detached := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusOK && status != http.StatusAccepted || detached.Result == nil {
		t.Fatalf("signed reply signal admission: %d %+v", status, detached)
	}
	saved := waitHTTPPinResult(t, &h2, r.RequestID, core.Accepted)
	assertReplySignalFacts(t, saved, r, nextReplySignal(t, observer, parent))
	native := setReplySignalNative(t, f.socket, parent, nil)
	if cancelled := nextReplySignal(t, observer, parent); cancelled.Action != native.Action || cancelled.Target != native.Target || !cancelled.ExpiresAt.Equal(native.ExpiresAt) {
		t.Fatal("independent native cancel mismatch", cancelled, native)
	}
	status, replay := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusAccepted || replay.Result == nil || replay.Result.ReplySignal == nil || *replay.Result.ReplySignal != *saved.ReplySignal {
		t.Fatalf("replay changed saved preparation: %d %+v", status, replay)
	}
	assertReplySignalFacts(t, *replay.Result, r, nil)
	assertNoReplySignal(t, observer)
	changed := r
	changed.ReplySignal = &core.ReplySignalInput{Note: "changed", TTLSeconds: 120}
	status, _, _ = headerReactionHTTP(t, &h2, http.MethodPost, "/v1/messages", requestJSON(t, changed))
	if status != http.StatusConflict {
		t.Fatalf("changed reply signal intent status=%d", status)
	}
}

func TestReplySignalHTTPFramedAndWebSocket(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newReplySignalObserver(t, f.socket, *target)
	parent := seedPinMutationMessage(t, f.socket, target.Name, "reply-signal-encrypted-parent")
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "reply-signal-frame")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "reply-signal-ws")
		}},
	} {
		for _, op := range []core.Operation{core.ReplySignalSet, core.ReplySignalCancel} {
			r := replySignalRequest("signal-"+call.name+"-"+strings.ReplaceAll(string(op), ".", "-"), parent, op, target)
			// Native gateway request IDs deliberately contain no periods.
			r.RequestID = strings.ReplaceAll(r.RequestID, "_", "-")
			status, result := call.fn(f.cred.token(t, r, true))
			if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil || !result.Result.Durable {
				t.Fatalf("%s %s admission: %d %+v", call.name, op, status, result)
			}
			assertReplySignalFacts(t, waitHTTPPinResult(t, f, r.RequestID, core.Accepted), r, nextReplySignal(t, observer, parent))
		}
	}
}
