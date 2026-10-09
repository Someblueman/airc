//go:build (darwin || linux) && arm64

package gateway_test

import (
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSharedMemoryAcceptedIntentReplayAfterGatewayKill(t *testing.T) {
	runNativeQueueAcceptedIntentReplayAfterGatewayKill(t, newSharedMemoryFixtureShutdown)
}

func TestMappedFileAcceptedIntentReplayAfterGatewayKill(t *testing.T) {
	runNativeQueueAcceptedIntentReplayAfterGatewayKill(t, newMappedFileFixtureShutdown)
}

func runNativeQueueAcceptedIntentReplayAfterGatewayKill(t *testing.T, newFixture func(*testing.T, bool) nativeQueueFixture) {
	t.Helper()
	f := newFixture(t, false)
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: f.kind + "-restart-intent", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "retained " + f.kind + " intent 世界", IssuedAt: &issued, ExpiresAt: &expiry}
	frame := sharedMemoryFrame(t, f, request, false)
	writeSharedMemoryFragments(t, f, sharedMemoryFragments(t, frame))
	first := readSharedMemoryResponse(t, f, time.Now().Add(5*time.Second))
	if first.Error != nil || first.Result == nil || first.Result.Result == nil || !first.Result.Result.Durable {
		t.Fatal("request did not reach durable intake", first)
	}
	receipt := pollSharedMemoryReceipt(t, f, request.RequestID)
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID == "" {
		t.Fatal("native acceptance missing before kill", receipt)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("native observer mismatch", seen)
	}
	if err := f.process.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err == nil {
			t.Fatal("killed gateway exited cleanly")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killed gateway did not exit")
	}
	p := startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, p)
	if name := gatewayAddress(t, p); name != f.address {
		t.Fatalf("restart changed %s address %q", f.kind, name)
	}
	// Retain the exact signed request; the new transport ID is only a delivery.
	writeSharedMemoryFragments(t, f, sharedMemoryFragments(t, frame))
	replayed := readSharedMemoryResponse(t, f, time.Now().Add(5*time.Second))
	if replayed.Error != nil || replayed.Result == nil || replayed.Result.Result == nil {
		t.Fatal("restart replay failed", replayed)
	}
	result := replayed.Result.Result
	if result.State != core.Accepted || result.SendAttempts != 1 || result.MessageID != receipt.MessageID {
		t.Fatal("restart changed the retained outcome", result, receipt)
	}
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(messages) != 1 || messages[0].ID != receipt.MessageID || messages[0].Message != request.Message {
		t.Fatal("restart replay duplicated or lost native intent", messages)
	}
}
