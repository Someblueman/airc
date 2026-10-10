//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMachIPCRejectsInvalidAuthStreamAndOversizedRequestsBeforeIntake(t *testing.T) {
	f := newMachIPCFixture(t, uint32(os.Geteuid()), "device")
	base := core.Request{Version: 1, RequestID: "mach-refusal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"}
	cases := []struct {
		name  string
		frame func() adapter.FrameRequest
	}{
		{name: "invalid-authentication", frame: func() adapter.FrameRequest {
			envelope := "invalid compact token"
			digest := sha256.Sum256([]byte(envelope))
			return adapter.FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
		}},
		{name: "signed-profile-refused", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f, base)
			frame.Profile = "signed"
			return frame
		}},
		{name: "correlation-mismatch", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f, base)
			frame.CorrelationID = "0000000000000000000000000000000000000000000000000000000000000000"
			return frame
		}},
		{name: "stream-refused", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f, base)
			frame.Stream = true
			return frame
		}},
		{name: "expired-envelope", frame: func() adapter.FrameRequest {
			now := time.Now().Unix()
			envelope := f.credentials.validityToken(t, base, now-600, now-300, true)
			return machFrameForEnvelope(envelope)
		}},
		{name: "wrong-audience", frame: func() adapter.FrameRequest {
			now := time.Now().Unix()
			realm := f.credentials.realm
			f.credentials.realm = "different-realm"
			envelope := f.credentials.validityToken(t, base, now, now+120, true)
			f.credentials.realm = realm
			return machFrameForEnvelope(envelope)
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
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, base.Target.Name); len(native) != 1 || native[0].ID != receipt.MessageID || native[0].Message != valid.Message {
		t.Fatalf("valid post-refusal native history=%+v", native)
	}
}

func machFrameForEnvelope(envelope string) adapter.FrameRequest {
	digest := sha256.Sum256([]byte(envelope))
	return adapter.FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
}

func TestMachIPCRefusesCallerUIDAndPrincipalMismatchBeforeIntake(t *testing.T) {
	uid := uint32(os.Geteuid())
	wrongUID := uid + 1
	if uid == ^uint32(0)-1 {
		wrongUID = uid - 1
	}
	t.Run("uid-mismatch", func(t *testing.T) {
		f := newMachIPCFixture(t, wrongUID, "device")
		frame := machFrame(t, f, core.Request{Version: 1, RequestID: "mach-wrong-uid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"})
		machNoReply(t, f, frame, false)
		machAssertNoIntake(t, f)
	})
	t.Run("principal-mismatch", func(t *testing.T) {
		f := newMachIPCFixture(t, uid, "alternate")
		frame := machFrame(t, f, core.Request{Version: 1, RequestID: "mach-wrong-principal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"})
		machNoReply(t, f, frame, false)
		machAssertNoIntake(t, f)
	})
}
