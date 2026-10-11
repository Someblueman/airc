//go:build darwin && arm64

package gateway_test

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMachIPCNativelySendsAndReconstructsBoundedHistory(t *testing.T) {
	f := newMachIPCFixture(t, uint32(os.Geteuid()))
	caps, _ := machCall(t, f, core.Request{Version: 1, RequestID: "mach-unary-capabilities", Operation: core.CapabilitiesGet})
	if caps.Result == nil || caps.Result.Capabilities == nil || len(caps.Result.Capabilities.WatchModes) != 0 {
		t.Fatalf("Mach IPC unary capabilities advertised watch modes: %+v", caps.Result)
	}
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	bodies := []string{
		"Mach IPC authenticated Καλημέρα 世界 🛰️",
		"Mach IPC full history record one " + strings.Repeat("λ", 1800),
		"Mach IPC full history record two " + strings.Repeat("λ", 1800),
		"Mach IPC full history record three " + strings.Repeat("λ", 1800),
		"Mach IPC full history record four " + strings.Repeat("λ", 1800),
	}
	observed := make(map[string]string, len(bodies))
	for i, body := range bodies {
		requestID := "mach-history-send-" + strconv.Itoa(i)
		response, _ := machCall(t, f, core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: target, Message: body})
		if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != requestID {
			t.Fatalf("Mach IPC durable send admission=%+v", response)
		}
		receipt := machAwaitAccepted(t, f, requestID)
		if receipt.SendAttempts != 1 || receipt.MessageID == "" {
			t.Fatalf("Mach IPC accepted receipt=%+v", receipt)
		}
		seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
		if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != body {
			t.Fatalf("Mach IPC native observer=%+v", seen)
		}
		observed[seen.ID] = seen.Message
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, target.Name)
	if len(native) != len(bodies) {
		t.Fatalf("Mach IPC independent native history count=%d want=%d: %+v", len(native), len(bodies), native)
	}
	for i, message := range native {
		if message.From != "fixture-author" || message.Target != target.Name || message.Message != bodies[i] || observed[message.ID] != message.Message {
			t.Fatalf("Mach IPC independent native history[%d]=%+v observer-body=%q", i, message, observed[message.ID])
		}
	}

	request := core.Request{Version: 1, RequestID: "mach-history-page", Operation: core.HistoryRead, Target: target, Start: "retained", Limit: 1}
	var collected []*irc.MessageMetadata
	complete := false
	for pageNumber := 0; pageNumber <= len(bodies); pageNumber++ {
		response, _ := machCall(t, f, request)
		if response.Event != "result" || response.Result == nil {
			t.Fatalf("Mach IPC history response=%+v", response)
		}
		page := response.Result.Page
		if page == nil || (page.Status != "ok" && page.Status != "more") || page.Gap || page.Cursor == "" || len(page.Messages) > 1 {
			t.Fatalf("Mach IPC bounded history page=%+v", page)
		}
		collected = append(collected, page.Messages...)
		request.Start, request.Cursor = "", page.Cursor
		if len(page.Messages) == 0 {
			complete = page.Status == "ok"
			break
		}
	}
	large, sealedBytes := machCall(t, f, core.Request{Version: 1, RequestID: "mach-history-large-reply", Operation: core.HistoryRead, Target: target, Start: "retained", Limit: len(bodies)})
	if !complete || !reflect.DeepEqual(collected, native) || large.Result == nil || large.Result.Page == nil || !reflect.DeepEqual(large.Result.Page.Messages, native) || sealedBytes <= 16<<10 {
		t.Fatalf("Mach IPC cursor/full-response complete=%v bounded=%+v large=%+v sealed-bytes=%d native=%+v", complete, collected, large.Result, sealedBytes, native)
	}
}

func TestMachIPCLostReplyRetryPreservesOneNativeSend(t *testing.T) {
	f := newMachIPCFixture(t, uint32(os.Geteuid()))
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	request := core.Request{Version: 1, RequestID: "mach-lost-reply-stable", Operation: core.Send, Target: target, Message: "retry the exact Mach intent 世界"}
	frame := machFrame(t, f, request)
	saved := machFrameBytes(t, frame)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if wire, err := runMachIPCClient(ctx, f.client, f.serviceName, saved, "--drop-reply"); err != nil || len(wire) != 0 {
		t.Fatalf("submit and abandon first Mach reply: bytes=%d err=%v", len(wire), err)
	}
	ctxRetry, cancelRetry := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelRetry()
	wire, err := runMachIPCClient(ctxRetry, f.client, f.serviceName, saved)
	if err != nil {
		t.Fatalf("retry exact saved Mach FrameRequest: %v", err)
	}
	response := machOpenResponse(t, f, wire, frame.CorrelationID)
	if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != request.RequestID {
		t.Fatalf("Mach IPC stable retry admission=%+v", response)
	}
	receipt := machAwaitAccepted(t, f, request.RequestID)
	if receipt.SendAttempts != 1 {
		t.Fatalf("Mach IPC retry duplicated native send attempts: %+v", receipt)
	}
	seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != request.Message {
		t.Fatalf("Mach IPC retry native event=%+v", seen)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, target.Name); len(native) != 1 || native[0].ID != receipt.MessageID || native[0].Message != request.Message {
		t.Fatalf("Mach IPC retry native history=%+v", native)
	}
}
