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

func TestPinMutationHeadersHTTP1AndHTTP2(t *testing.T) {
	f := newReactionTransportFixture(t)
	parent := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "pin-http-parent")
	request := core.Request{Version: 1, RequestID: "pin-http1", Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent}
	roomHeaderPost(t, f, request, 1)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("native pin missing: %v", pins)
	}
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	request.RequestID, request.Operation = "pin-http2-remove", core.PinsRemove
	roomHeaderPost(t, &h2, request, 2)
	assertPinMutationFacts(t, waitHTTPPinResult(t, &h2, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("native unpin missing: %v", pins)
	}
	request.RequestID, request.Operation = "pin-signed-http2", core.PinsAdd
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request.IssuedAt, request.ExpiresAt = &issued, &expires
	token := f.cred.token(t, request, false)
	status, detached := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusOK && status != http.StatusAccepted || detached.Result == nil {
		t.Fatalf("signed HTTP2 pin admission: %d %+v", status, detached)
	}
	saved := waitHTTPPinResult(t, &h2, request.RequestID, core.Accepted)
	assertPinMutationFacts(t, saved, request)
	removeNativePin(t, f, parent)
	status, replay := contextHTTPCall(t, &h2, token, false)
	if status != http.StatusAccepted || replay.Result == nil || replay.Result.Pin == nil || *replay.Result.Pin != *saved.Pin {
		t.Fatalf("replay changed pin acknowledgement: %d %+v", status, replay)
	}
	assertPinMutationFacts(t, *replay.Result, request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("accepted replay repinned message: %v", pins)
	}
	changed := request
	changed.Operation = core.PinsRemove
	status, _, _ = headerReactionHTTP(t, &h2, http.MethodPost, "/v1/messages", requestJSON(t, changed))
	if status != http.StatusConflict {
		t.Fatalf("changed pin intent status=%d", status)
	}
}

func TestPinMutationAcrossHTTPFramedAndWebSocket(t *testing.T) {
	f := newReactionTransportFixture(t)
	parent := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "pin-encrypted-parent")
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "pin-frame")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "pin-ws")
		}},
	} {
		for _, operation := range []core.Operation{core.PinsAdd, core.PinsRemove} {
			request := core.Request{Version: 1, RequestID: "pin-" + call.name + "-" + strings.ReplaceAll(string(operation), ".", "-"), Operation: operation, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent}
			status, result := call.fn(f.cred.token(t, request, true))
			if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil || !result.Result.Durable {
				t.Fatalf("%s %s admission: %d %+v", call.name, operation, status, result)
			}
			assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
			pins := queryPinMutationNative(t, f.socket, request.Target.Name)
			if operation == core.PinsAdd && (len(pins) != 1 || pins[0].ID != parent) || operation == core.PinsRemove && len(pins) != 0 {
				t.Fatalf("%s %s native effect: %v", call.name, operation, pins)
			}
		}
	}
}
