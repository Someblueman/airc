//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryMailboxNativeModeRefusals(t *testing.T) {
	t.Parallel()
	f := newDirectoryMailboxFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, kind := range []string{"stream", "name", "profile", "version"} {
		request := core.Request{Version: 1, RequestID: "directory-refuse-" + kind, Operation: core.Send, Target: room, Message: "must never reach native history"}
		frame := frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain"}
		frame.Request = f.credentials.token(t, request, false)
		expected := core.InvalidRequest
		switch kind {
		case "stream":
			frame.Stream = true
			expected = core.InvalidRequest
		case "name":
			frame.CorrelationID = "other-name"
			expected = core.InvalidRequest
		case "profile":
			frame.Profile = "authorSpec"
			expected = core.InvalidRequest
		case "version":
			frame.Version = 2
			expected = core.InvalidRequest
		}
		data, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		response := directoryMailboxDocument(t, f, request.RequestID, data)
		if response.CorrelationID != frame.CorrelationID || response.Error == nil || response.Error.Code != expected {
			t.Fatal("wrong actual directory refusal", kind, response)
		}
		noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 0 {
		t.Fatal("refused directory frames changed native history", native)
	}
	request := core.Request{Version: 1, RequestID: "directory-healthy-after-refusals", Operation: core.Send, Target: room, Message: "healthy directory after refusal 🙂"}
	if initial := directoryMailboxCall(t, f, request, true).Result.Result; initial == nil || !initial.Durable {
		t.Fatal("refusal blocked healthy intake", initial)
	}
	var receipt *core.Result
	deadline := time.Now().Add(3 * time.Second)
	for {
		receipt = directoryMailboxCall(t, f, core.Request{Version: 1, RequestID: "directory-healthy-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, false).Result.Result
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
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("refusals duplicated healthy native record", native)
	}
}
