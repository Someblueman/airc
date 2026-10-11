//go:build linux && arm64

package gateway_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSCTPLostResponseReplayAfterGatewayRestartDoesNotResend(t *testing.T) {
	runSCTPLostResponseReplayAfterGatewayRestart(t, adapter.SCTPOrdered)
}

func TestSCTPUnorderedLostResponseReplayAfterGatewayRestartDoesNotResend(t *testing.T) {
	runSCTPLostResponseReplayAfterGatewayRestart(t, adapter.SCTPUnordered)
}

func TestSCTPPRTTLLostResponseReplayAfterGatewayRestartDoesNotResend(t *testing.T) {
	runSCTPLostResponseReplayAfterGatewayRestart(t, adapter.SCTPOrderedTTL)
}

func runSCTPLostResponseReplayAfterGatewayRestart(t *testing.T, mode adapter.SCTPMode) {
	f := newSCTPFixtureMode(t, "ipv4", mode, false)
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "sctp-restart-stable-intent", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "SCTP response lost across restart 世界", IssuedAt: &issued, ExpiresAt: &expiry}
	frame := sctpFrame(t, f, request, true)
	sctpSendFrame(t, f.client, frame)
	first := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == request.Message })
	if first.From != "fixture-author" || first.Target != request.Target.Name {
		t.Fatalf("initial native SCTP post=%+v", first)
	}
	// ReceiptGet runs over another association so the original response remains
	// unread while delivery reaches its durable accepted state.
	receiptClient := dialSCTP(t, f.address, f.mode)
	deadline := time.Now().Add(5 * time.Second)
	var receipt core.Result
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		lookup := core.Request{Version: 1, RequestID: "sctp-replay-receipt-" + strconv.Itoa(attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := sctpCall(t, f, receiptClient, lookup)
		if response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("initial durable receipt lookup=%+v", response)
		}
		receipt = *response.Result.Result
		if receipt.State == core.Accepted {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID != first.ID {
		t.Fatalf("initial SCTP durable receipt=%+v native=%+v", receipt, first)
	}
	if err := receiptClient.close(); err != nil {
		t.Fatal(err)
	}
	// Drop the client association without consuming its response. This leaves
	// the signed stable intent as the only replay authority.
	if err := f.client.close(); err != nil {
		t.Fatal(err)
	}
	if err := f.process.command.Process.Kill(); err != nil {
		t.Fatalf("kill gateway after durable native post: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("SIGKILLed gateway exited cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killed gateway did not exit and join")
	}

	restarted := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, restarted)
	f.process = restarted
	f.address = gatewayAddress(t, restarted)
	f.client = dialSCTP(t, f.address, f.mode)
	sctpSendFrame(t, f.client, frame)
	replayed := sctpReadResponse(t, f, f.client, frame.CorrelationID, time.Now().Add(8*time.Second))
	if replayed.Event != "result" || replayed.Sequence != 1 || replayed.Result == nil || replayed.Result.Result == nil {
		t.Fatalf("SCTP retained intent replay=%+v", replayed)
	}
	result := replayed.Result.Result
	if result.State != core.Accepted || result.SendAttempts != 1 || result.MessageID != first.ID {
		t.Fatalf("SCTP replay changed durable result=%+v first_message=%q", result, first.ID)
	}
	messages := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if len(messages) != 1 || messages[0].ID != first.ID || messages[0].From != "fixture-author" || messages[0].Target != request.Target.Name || messages[0].Message != request.Message {
		t.Fatalf("SCTP restart replay duplicated or changed native post: %+v", messages)
	}
}
