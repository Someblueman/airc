//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPinMutationNativeQuotaAndMute(t *testing.T) {
	f := newReactionTransportFixtureWithAdmin(t, true)
	request := core.Request{Version: 1, Operation: core.PinsAdd, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	for i := 0; i < 6; i++ {
		request.RequestID = fmt.Sprintf("pin-limit-%d", i)
		request.ParentID = seedPinMutationMessage(t, f.socket, request.Target.Name, fmt.Sprintf("pin-limit-parent-%d", i))
		roomHeaderPost(t, f, request, 1)
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
	admin := roomWaitingAdmin(t, f)
	defer admin.Close()
	moderate := func(action string) {
		if err := admin.Moderate(irc.AdminRequest{Action: action, Nick: "fixture-author", Scope: request.Target.Name}); err != nil {
			t.Fatal(err)
		}
		ack := next(t, admin, func(e *irc.AdminEvent) bool { return e.Action == action })
		if !ack.Changed {
			t.Fatalf("native %s control made no change: %+v", action, ack)
		}
		next(t, admin, func(e *irc.EndOfAdminEvent) bool { return true })
	}
	moderate("mute")
	request.RequestID, request.Operation, request.ParentID = "pin-muted-remove", core.PinsRemove, pins[0].ID
	roomHeaderPost(t, f, request, 1)
	blocked := waitHTTPPinResult(t, f, request.RequestID, core.Blocked)
	if blocked.Pin != nil || blocked.Accepted == nil || *blocked.Accepted || blocked.AcceptanceUncertain || blocked.Failure == nil || blocked.Failure.Code != core.Forbidden || blocked.Failure.UpstreamCode != 485 {
		t.Fatalf("native mute did not block pin write: %+v", blocked)
	}
	if after := queryPinMutationNative(t, f.socket, request.Target.Name); !assertSamePins(core.PinsResult{Target: *request.Target, Pins: pins}, core.PinsResult{Target: *request.Target, Pins: after}) {
		t.Fatalf("muted remove changed pins: %v", after)
	}
	moderate("unmute")
	request.RequestID = "pin-unmuted-remove"
	roomHeaderPost(t, f, request, 1)
	assertPinMutationFacts(t, waitHTTPPinResult(t, f, request.RequestID, core.Accepted), request)
	if after := queryPinMutationNative(t, f.socket, request.Target.Name); len(after) != 4 {
		t.Fatalf("unmuted remove did not recover: %v", after)
	}
}
