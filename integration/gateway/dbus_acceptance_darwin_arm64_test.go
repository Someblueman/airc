//go:build darwin && arm64

package gateway_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDBusBridgeForwardsNativeSendAndBoundedHistory(t *testing.T) {
	f := newDBusFixture(t, xpcCallerUID(), "device")
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	bodies := []string{
		"D-Bus peer Καλημέρα 世界 🛰️",
		"D-Bus history one " + strings.Repeat("λ", 1800),
		"D-Bus history two " + strings.Repeat("λ", 1800),
		"D-Bus history three " + strings.Repeat("λ", 1800),
		"D-Bus history four " + strings.Repeat("λ", 1800),
	}
	observed := make(map[string]string, len(bodies))
	for i, body := range bodies {
		requestID := "dbus-history-send-" + strconv.Itoa(i)
		response, _ := dbusCall(t, f, core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: target, Message: body})
		if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != requestID {
			t.Fatalf("D-Bus durable send admission=%+v", response)
		}
		receipt := machAwaitAccepted(t, f.machIPCFixture, requestID)
		if receipt.SendAttempts != 1 || receipt.MessageID == "" {
			t.Fatalf("D-Bus accepted receipt=%+v", receipt)
		}
		seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
		if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != body {
			t.Fatalf("D-Bus native observer=%+v", seen)
		}
		observed[seen.ID] = seen.Message
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, target.Name)
	if len(native) != len(bodies) {
		t.Fatalf("D-Bus independent native history count=%d want=%d: %+v", len(native), len(bodies), native)
	}
	for i, message := range native {
		if message.From != "fixture-author" || message.Target != target.Name || message.Message != bodies[i] || observed[message.ID] != message.Message {
			t.Fatalf("D-Bus independent native history[%d]=%+v observer-body=%q", i, message, observed[message.ID])
		}
	}

	request := core.Request{Version: 1, RequestID: "dbus-history-page", Operation: core.HistoryRead, Target: target, Start: "retained", Limit: 1}
	var collected []*irc.MessageMetadata
	complete := false
	for pageNumber := 0; pageNumber <= len(bodies); pageNumber++ {
		response, _ := dbusCall(t, f, request)
		if response.Event != "result" || response.Result == nil {
			t.Fatalf("D-Bus history response=%+v", response)
		}
		page := response.Result.Page
		if page == nil || (page.Status != "ok" && page.Status != "more") || page.Gap || page.Cursor == "" || len(page.Messages) > 1 {
			t.Fatalf("D-Bus bounded history page=%+v", page)
		}
		collected = append(collected, page.Messages...)
		request.Start, request.Cursor = "", page.Cursor
		if len(page.Messages) == 0 {
			complete = page.Status == "ok"
			break
		}
	}
	large, sealedBytes := dbusCall(t, f, core.Request{Version: 1, RequestID: "dbus-history-large", Operation: core.HistoryRead, Target: target, Start: "retained", Limit: len(bodies)})
	if !complete || !reflect.DeepEqual(collected, native) || large.Result == nil || large.Result.Page == nil || !reflect.DeepEqual(large.Result.Page.Messages, native) || sealedBytes <= 16<<10 {
		t.Fatalf("D-Bus cursor/full-response complete=%v collected=%+v large=%+v sealed-bytes=%d native=%+v", complete, collected, large.Result, sealedBytes, native)
	}
}

func TestDBusBridgeRetryAfterDiscardedClientReceiptPreservesOneNativeSend(t *testing.T) {
	f := newDBusFixture(t, xpcCallerUID(), "device")
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "dbus-lost-client-receipt", Operation: core.Send, Target: target, Message: "retry exact D-Bus intent 世界"}
	frame := machFrame(t, f.machIPCFixture, request)
	saved := machFrameBytes(t, frame)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if output, err := runDBusClient(ctx, f.client, f.socket, saved, true); err != nil || len(output) != 0 {
		t.Fatalf("discard first D-Bus client receipt: bytes=%d err=%v", len(output), err)
	}
	response, _ := dbusCallFrame(t, f, frame)
	if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != request.RequestID {
		t.Fatalf("D-Bus exact retry admission=%+v", response)
	}
	receipt := machAwaitAccepted(t, f.machIPCFixture, request.RequestID)
	if receipt.SendAttempts != 1 {
		t.Fatalf("D-Bus exact retry duplicated native send attempts: %+v", receipt)
	}
	seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != request.Message {
		t.Fatalf("D-Bus retry native event=%+v", seen)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, target.Name); len(native) != 1 || native[0].ID != receipt.MessageID || native[0].Message != request.Message {
		t.Fatalf("D-Bus retry native history=%+v", native)
	}
}
