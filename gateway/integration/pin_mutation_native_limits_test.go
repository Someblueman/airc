//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

// TestPinMutationNativeQuota maps the native five-pin limit to a permanent
// rejection that changes nothing.
func TestPinMutationNativeQuota(t *testing.T) {
	f := newReactionHTTPFixture(t)
	request := core.Request{Version: 1, Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	for i := 0; i < 6; i++ {
		request.RequestID = fmt.Sprintf("pin-limit-%d", i)
		request.ParentID = seedPinMutationMessage(t, f.socket, request.Target.Name, fmt.Sprintf("pin-limit-parent-%d", i))
		headerPost(t, f, request)
		if i < 5 {
			assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
		} else {
			rejected := waitHTTPPinResult(t, f, request.RequestID, core.Rejected)
			if rejected.Pin != nil || rejected.Accepted == nil || *rejected.Accepted || rejected.AcceptanceUncertain || rejected.Failure == nil || rejected.Failure.Code != core.InvalidRequest || rejected.Failure.UpstreamCode != 461 {
				t.Fatalf("native sixth-pin limit: %+v", rejected)
			}
		}
	}
	pins := queryPinMutationNative(t, f.socket, request.Target.Name)
	if len(pins) != 5 {
		t.Fatalf("native quota did not retain five pins: %v", pins)
	}
}
