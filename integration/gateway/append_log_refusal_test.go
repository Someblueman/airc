//go:build darwin || linux

package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAppendLogNativeAuthenticationAndModeRefusals(t *testing.T) {
	f := newAppendLogFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, kind := range []string{"signature", "expired", "future", "audience", "target", "stream", "correlation", "profile", "version"} {
		request := core.Request{Version: 1, RequestID: "append-log-refuse-" + kind, Operation: core.Send, Target: room, Message: "must never reach native history"}
		frame := adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "signed"}
		frame.Envelope = f.credentials.token(t, request, false)
		now := time.Now().Unix()
		expected := core.AuthenticationFailed
		switch kind {
		case "signature":
			parts := strings.Split(frame.Envelope, ".")
			if len(parts) != 3 {
				t.Fatal("fixture did not produce JWS")
			}
			signature, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil || len(signature) != 64 {
				t.Fatal("wrong Ed25519 signature", err)
			}
			signature[0] ^= 1
			parts[2] = base64.RawURLEncoding.EncodeToString(signature)
			frame.Envelope = strings.Join(parts, ".")
		case "expired":
			frame.Envelope = f.credentials.validityToken(t, request, now-600, now-300, false)
		case "future":
			frame.Envelope = f.credentials.validityToken(t, request, now+300, now+600, false)
		case "audience":
			other := *f.credentials
			other.realm = "other-realm"
			frame.Envelope = other.token(t, request, false)
		case "target":
			request.Target = &core.Target{Kind: "room", Name: "#not-authorized"}
			frame.Envelope = f.credentials.token(t, request, false)
			expected = core.Forbidden
		case "stream":
			frame.Stream = true
			expected = core.InvalidRequest
		case "correlation":
			frame.CorrelationID = ""
			expected = core.InvalidRequest
		case "profile":
			frame.Profile = "bearer"
			expected = core.InvalidRequest
		case "version":
			frame.Version = 2
			expected = core.InvalidRequest
		}
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		response := appendLogDocument(t, f, request.RequestID, data)
		if response.CorrelationID != frame.CorrelationID || response.Error == nil || response.Error.Code != expected {
			t.Fatal("wrong actual append-log refusal", kind, response)
		}
		noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 0 {
		t.Fatal("refused append-log frames changed native history", native)
	}
	request := core.Request{Version: 1, RequestID: "append-log-healthy-after-refusals", Operation: core.Send, Target: room, Message: "healthy append-log after refusal 🙂"}
	if initial := appendLogCall(t, f, request, true).Result.Result; initial == nil || !initial.Durable {
		t.Fatal("refusal blocked healthy intake", initial)
	}
	var receipt *core.Result
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt = appendLogCall(t, f, core.Request{Version: 1, RequestID: "append-log-healthy-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
		if receipt != nil && receipt.State == core.Accepted {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("healthy delivery did not recover", receipt)
		}
	}
	if receipt.SendAttempts != 1 {
		t.Fatal("unexpected healthy attempt count", receipt)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
		t.Fatal("healthy observer mismatch", event)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("refusals duplicated healthy native record", native)
	}
}
