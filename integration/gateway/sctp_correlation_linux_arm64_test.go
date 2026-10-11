//go:build linux && arm64

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSCTPRejectsSwappedCorrelationsBeforeIntake(t *testing.T) {
	for _, mode := range []adapter.SCTPMode{adapter.SCTPOrdered, adapter.SCTPUnordered, adapter.SCTPOrderedTTL} {
		name := map[adapter.SCTPMode]string{adapter.SCTPOrdered: "ordered", adapter.SCTPUnordered: "unordered", adapter.SCTPOrderedTTL: "pr-ttl"}[mode]
		t.Run(name, func(t *testing.T) {
			f := newSCTPFixtureMode(t, "ipv4", mode, true)
			var frames [2]frameRequest
			for i, id := range []string{"sctp-intent-one", "sctp-intent-two"} {
				frames[i] = sctpFrame(t, f, core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: id}, true)
			}
			if frames[0].CorrelationID == frames[1].CorrelationID {
				t.Fatal("distinct encrypted intents have the same correlation")
			}
			frames[0].CorrelationID, frames[1].CorrelationID = frames[1].CorrelationID, frames[0].CorrelationID
			for _, frame := range frames {
				assertSCTPInvalidIngressCloses(t, f, frame)
			}
			legacyRequest := core.Request{Version: 1, RequestID: "sctp-legacy-label", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "legacy label"}
			legacy := sctpFrame(t, f, legacyRequest, true)
			legacy.CorrelationID = "sctp-" + legacyRequest.RequestID
			assertSCTPInvalidIngressCloses(t, f, legacy)
			assertSCTPNoNativeIntake(t, f)
		})
	}
}
