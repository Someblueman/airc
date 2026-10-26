//go:build linux

package gateway_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUnixPacketNativeRecordSizeProfileAndTargetRefusals(t *testing.T) {
	f := newUnixPacketFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for _, kind := range []string{"unknown-profile", "target", "oversize", "partial"} {
		request := core.Request{Version: 1, RequestID: "record-refuse-" + kind, Operation: core.Send, Target: room, Message: "must never reach AIRC"}
		profile := "plain"
		expected := core.InvalidRequest
		if kind == "unknown-profile" {
			profile = "unknown"
		}
		if kind == "target" {
			request.Target = &core.Target{Kind: "room", Name: "#not-authorized"}
			expected = core.Forbidden
		}
		body, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: profile, Request: f.credentials.token(t, request, false)})
		if err != nil {
			t.Fatal(err)
		}
		conn := unixPacketDial(t, f)
		switch kind {
		case "oversize":
			if len(body) > adapter.MaxRequestFrameBytes {
				t.Fatal("fixture token already exceeds record bound")
			}
			body = append(body, []byte(strings.Repeat(" ", adapter.MaxRequestFrameBytes+1-len(body)))...)
		case "partial":
			body = body[:len(body)/2]
		}
		unixPacketWrite(t, conn, body)
		if kind == "oversize" || kind == "partial" {
			if n, _, _, _, err := conn.ReadMsgUnix(make([]byte, 1), nil); n != 0 || err != io.EOF {
				t.Fatal("invalid record did not close before intake", kind, n, err)
			}
		} else {
			response := unixPacketRead(t, conn)
			if response.CorrelationID != request.RequestID || response.Error == nil || response.Error.Code != expected {
				t.Fatal("record profile/target refusal mismatch", kind, response)
			}
		}
		_ = conn.Close()
		noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 0 {
		t.Fatal("refused records reached native history", native)
	}
	request := core.Request{Version: 1, RequestID: "record-exact-limit", Operation: core.Send, Target: room, Message: "healthy exact-limit Unicode record 🙂"}
	body, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatal("invalid exact-limit fixture", err)
	}
	body = append(body, []byte(strings.Repeat(" ", adapter.MaxRequestFrameBytes-len(body)))...)
	conn := unixPacketDial(t, f)
	unixPacketWrite(t, conn, body)
	response := unixPacketRead(t, conn)
	_ = conn.Close()
	if response.CorrelationID != request.RequestID || response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatal("healthy exact-limit record was refused", response)
	}
	receipt := unixPacketReceipt(t, f, request.RequestID)
	if receipt.SendAttempts != 1 {
		t.Fatal("exact-limit send attempts", receipt)
	}
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if event.From != "fixture-author" || event.Target != room.Name || event.Message != request.Message {
		t.Fatal("exact-limit independent native observer mismatch", event)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 1 || native[0].ID != receipt.MessageID {
		t.Fatal("refusal or exact-limit acceptance changed native record count", native)
	}
	f.stop(t)
}
