//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestActivityMutationBearerHTTP1HTTP2SignedAndNativeThrottle(t *testing.T) {
	f := newReactionTransportFixture(t)
	target := core.Target{Kind: "room", Name: "#gateway-fixture"}
	observer := newActivityObserver(t, f.socket, target)
	request := core.Request{Version: 1, RequestID: "activity-http1", Operation: core.ActivitySet, Target: &target, Activity: &core.ActivityInput{Kind: "typing", Note: "first note", TTLSeconds: 15}}
	roomBearerPost(t, f, request, 1)
	first := waitHTTPPinResult(t, f, request.RequestID, core.Accepted)
	assertActivityMutationFacts(t, first, request, nextActivitySignal(t, observer))
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	request.RequestID = "activity-http2-throttled"
	request.Activity = &core.ActivityInput{Kind: "thinking", Note: "different note", TTLSeconds: 1}
	roomBearerPost(t, &h2, request, 2)
	throttled := waitHTTPPinResult(t, &h2, request.RequestID, core.Accepted)
	assertActivityMutationFacts(t, throttled, request, nil)
	if *throttled.Activity != *first.Activity {
		t.Fatalf("native cross-kind throttle was not preserved: first=%+v later=%+v", first.Activity, throttled.Activity)
	}
	assertNoActivitySignal(t, observer)
	// A fresh request after the native two-second throttle must apply new intent.
	time.Sleep(2100 * time.Millisecond)
	request.RequestID = "activity-signed-http2"
	request.Activity = &core.ActivityInput{Kind: "thinking", Note: "fresh signed note", TTLSeconds: 15}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request.IssuedAt, request.ExpiresAt = &issued, &expires
	token := f.cred.token(t, request, false)
	status, detached := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusOK && status != http.StatusAccepted || detached.Result == nil {
		t.Fatalf("signed activity admission: %d %+v", status, detached)
	}
	saved := waitHTTPPinResult(t, &h2, request.RequestID, core.Accepted)
	assertActivityMutationFacts(t, saved, request, nextActivitySignal(t, observer))
	if saved.Activity.Kind != request.Activity.Kind || saved.Activity.Note != request.Activity.Note {
		t.Fatal("fresh signal did not apply requested intent", saved.Activity)
	}
	status, replay := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusOK || replay.Result == nil || replay.Result.Activity == nil || *replay.Result.Activity != *saved.Activity {
		t.Fatalf("signed activity replay changed evidence: %d %+v", status, replay)
	}
	assertActivityMutationFacts(t, *replay.Result, request, nil)
	assertNoActivitySignal(t, observer)
	changed := request
	changed.Activity = &core.ActivityInput{Kind: "typing", TTLSeconds: 15}
	status, _, _ = bearerReactionHTTP(t, &h2, http.MethodPost, "/v1/messages", requestJSON(t, changed))
	if status != http.StatusConflict {
		t.Fatalf("changed activity intent status=%d", status)
	}
}

func TestActivityMutationEncryptedHTTPFramedWebSocketAndDirectTarget(t *testing.T) {
	direct := core.Target{Kind: "direct", Name: "activity-recipient"}
	f := newReactionTransportFixtureWithTargets(t, false, []core.Target{direct})
	room := core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, call := range []struct {
		name   string
		target core.Target
		fn     func(string) (int, dispatch.DetachedResult)
	}{
		{"http", room, func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", room, func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "activity-frame")
		}},
		{"websocket", room, func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "activity-ws")
		}},
		{"direct-http", direct, func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
	} {
		if call.name != "http" {
			time.Sleep(2100 * time.Millisecond)
		}
		observer := newActivityObserver(t, f.socket, call.target)
		request := core.Request{Version: 1, RequestID: "activity-" + call.name, Operation: core.ActivitySet, Target: &call.target, Activity: &core.ActivityInput{Kind: "typing", Note: call.name, TTLSeconds: 15}}
		status, result := call.fn(f.cred.token(t, request, true))
		if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil || !result.Result.Durable {
			t.Fatalf("%s activity admission: %d %+v", call.name, status, result)
		}
		assertActivityMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request, nextActivitySignal(t, observer))
		_ = observer.Close()
	}
}
