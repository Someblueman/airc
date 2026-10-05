//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUDPAllSharedOperations(t *testing.T) {
	f := newUDPFixture(t)
	checkUDPReadOnlyCLI(t, f)
	call := func(request core.Request, encrypted bool) adapter.FrameResponse {
		return udpDatagramCall(t, f, request, encrypted)
	}
	accepted := func(request core.Request, encrypted bool) core.Result {
		return udpDatagramAccepted(t, f, request, encrypted)
	}
	exerciseNativeOperations(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, "udp-datagram")
}

func checkUDPReadOnlyCLI(t *testing.T, f udpFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := f.process.command.Args
	validate := exec.CommandContext(ctx, args[0], "config", "validate", "--file", args[len(args)-1])
	output, err := validate.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "configuration valid; no runtime or listeners started" {
		t.Fatalf("UDP config CLI: %v %s", err, output)
	}
	capabilities := exec.CommandContext(ctx, args[0], "capabilities", "--file", args[len(args)-1])
	output, err = capabilities.CombinedOutput()
	var result struct {
		Runtime   string `json:"runtime"`
		Listeners []struct {
			ID         string           `json:"id"`
			Status     string           `json:"status"`
			Operations []core.Operation `json:"operations"`
		} `json:"listeners"`
	}
	if err != nil || json.Unmarshal(output, &result) != nil || result.Runtime != "partial_not_running" || len(result.Listeners) != 1 || result.Listeners[0].ID != "udp" || result.Listeners[0].Status != "enabled_udp_partial" || !reflect.DeepEqual(result.Listeners[0].Operations, f.config.Listeners[0].Operations) {
		t.Fatalf("UDP capabilities CLI: %v %s", err, output)
	}
}
