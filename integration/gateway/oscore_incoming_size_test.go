//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOSCOREIncomingBodyLimitAndRecovery(t *testing.T) {
	f := newOSCOREFixture(t)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "oscore-exact-input-limit", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "exact 16 KiB protected JSON input 🙂", IssuedAt: &issued, ExpiresAt: &expires}
	command, sequence, output := oscoreNativeCommand(t, f, oscoreInput(t, f, request, 16384), f.address, oscoreSequence(t, f.sequence), 1024)
	if diagnostic, err := command.CombinedOutput(); err != nil {
		t.Fatalf("exact-limit native input failed: %v %s", err, diagnostic)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var admitted adapter.FrameResponse
	if err := json.Unmarshal(data, &admitted); err != nil || admitted.Result == nil || admitted.Result.Result == nil || !admitted.Result.Result.Durable || admitted.Result.Result.RequestID != request.RequestID {
		t.Fatal("exact native input limit not admitted", admitted, err)
	}
	resumeOSCORESequence(t, f, sequence)
	accepted := oscoreAccepted(t, f, request)
	request.RequestID, request.Message = "oscore-over-input-limit", "oversized protected frame must never reach intake"
	command, sequence, _ = oscoreNativeCommand(t, f, oscoreInput(t, f, request, 16385), f.address, oscoreSequence(t, f.sequence), 1024)
	diagnostic, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(diagnostic), "4.13") || !strings.Contains(string(diagnostic), "Max body size 16384") {
		t.Fatalf("oversized native input was not refused: %v %s", err, diagnostic)
	}
	noOSCOREJob(t, f, request.RequestID)
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(native) != 1 || native[0].ID != accepted.MessageID || native[0].From != "fixture-author" {
		t.Fatal("oversized native input changed AIRC records", native)
	}
	resumeOSCORESequence(t, f, sequence)
	request.RequestID, request.Message = "oscore-after-input-limit", "healthy input after 4.13 🙂"
	recovered := oscoreAccepted(t, f, request)
	native = nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(native) != 2 || native[1].ID != recovered.MessageID || native[1].From != "fixture-author" || native[1].Message != request.Message {
		t.Fatal("native input limit prevented healthy recovery", native)
	}
}
