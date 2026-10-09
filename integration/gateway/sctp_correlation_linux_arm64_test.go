//go:build linux && arm64

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSCTPUnorderedRejectsSwappedEnvelopeCorrelationsBeforeIntake(t *testing.T) {
	f := newSCTPFixtureMode(t, "ipv4", true, true)
	var frames [2]adapter.FrameRequest
	for i, id := range []string{"unordered-intent-one", "unordered-intent-two"} {
		frames[i] = sctpFrame(t, f, core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: id}, true)
	}
	if frames[0].CorrelationID == frames[1].CorrelationID {
		t.Fatal("distinct encrypted intents have the same correlation")
	}
	frames[0].CorrelationID, frames[1].CorrelationID = frames[1].CorrelationID, frames[0].CorrelationID
	for _, frame := range frames {
		assertSCTPInvalidIngressCloses(t, f, frame)
	}
	assertSCTPNoNativeIntake(t, f)
}
