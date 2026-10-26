//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

// TestPinMutationHTTP maps pins.add and pins.remove to the native room pin
// list; the saved acknowledgement is exactly the requested pin change.
func TestPinMutationHTTP(t *testing.T) {
	f := newReactionHTTPFixture(t)
	parent := seedPinMutationMessage(t, f.socket, "#gateway-fixture", "pin-http-parent")
	request := core.Request{Version: 1, RequestID: "pin-http-add", Operation: core.PinsAdd, Target: contractRoom(), ParentID: parent}
	headerPost(t, f, request)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 1 || pins[0].ID != parent {
		t.Fatalf("native pin missing: %v", pins)
	}
	request.RequestID, request.Operation = "pin-http-remove", core.PinsRemove
	if status, admission := reactionHTTPCall(t, f, f.cred.token(t, request, true)); status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil || !admission.Result.Durable {
		t.Fatalf("request-route unpin admission: %d %+v", status, admission)
	}
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if pins := queryPinMutationNative(t, f.socket, request.Target.Name); len(pins) != 0 {
		t.Fatalf("native unpin missing: %v", pins)
	}
}
