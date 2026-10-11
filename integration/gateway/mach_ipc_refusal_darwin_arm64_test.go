//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMachIPCRejectsInvalidAuthStreamAndOversizedRequestsBeforeIntake(t *testing.T) {
	f := newMachIPCFixture(t, uint32(os.Geteuid()))
	base := core.Request{Version: 1, RequestID: "mach-refusal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"}
	cases := []struct {
		name  string
		frame func() frameRequest
	}{
		{name: "invalid-authentication", frame: func() frameRequest {
			requestBody := "[]"
			digest := sha256.Sum256([]byte(requestBody))
			return frameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "plain", Request: requestBody}
		}},
		{name: "signed-profile-refused", frame: func() frameRequest {
			frame := machFrame(t, f, base)
			frame.Profile = "unknown"
			return frame
		}},
		{name: "correlation-mismatch", frame: func() frameRequest {
			frame := machFrame(t, f, base)
			frame.CorrelationID = "0000000000000000000000000000000000000000000000000000000000000000"
			return frame
		}},
		{name: "stream-refused", frame: func() frameRequest {
			frame := machFrame(t, f, base)
			frame.Stream = true
			return frame
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machNoReply(t, f, tc.frame(), false)
			machAssertNoIntake(t, f)
		})
	}
	oversized := append(machFrameBytes(t, machFrame(t, f, base)), bytes.Repeat([]byte(" "), (16<<10)+1)...)
	oversized = oversized[:(16<<10)+1]
	machNoReplyWire(t, f, oversized, true)
	machAssertNoIntake(t, f)
	valid := core.Request{Version: 1, RequestID: "mach-after-oversize", Operation: core.Send, Target: base.Target, Message: "listener remains usable after oversize refusal"}
	frame := machFrame(t, f, valid)
	response, _ := machCallFrame(t, f, frame)
	if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("valid intent after oversized refusal was not admitted: %+v", response)
	}
	receipt := machAwaitAccepted(t, f, valid.RequestID)
	if receipt.SendAttempts != 1 {
		t.Fatalf("valid post-refusal receipt=%+v", receipt)
	}
	seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != base.Target.Name || seen.Message != valid.Message {
		t.Fatalf("valid post-refusal native event=%+v", seen)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, base.Target.Name); len(native) != 1 || native[0].ID != receipt.MessageID || native[0].Message != valid.Message {
		t.Fatalf("valid post-refusal native history=%+v", native)
	}
}

func TestMachIPCRefusesCallerUIDMismatchBeforeIntake(t *testing.T) {
	uid := uint32(os.Geteuid())
	wrongUID := uid + 1
	if uid == ^uint32(0)-1 {
		wrongUID = uid - 1
	}
	t.Run("uid-mismatch", func(t *testing.T) {
		f := newMachIPCFixture(t, wrongUID)
		frame := machFrame(t, f, core.Request{Version: 1, RequestID: "mach-wrong-uid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"})
		machNoReply(t, f, frame, false)
		machAssertNoIntake(t, f)
	})
}
