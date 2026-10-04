//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func framedExample(t *testing.T, address, ca string, credentials *detachedCredentials) {
	t.Helper()
	profile := "signed"
	args := []string{"run", "./examples/gateway/framed-client", "--address", address, "--correlation", "example"}
	if ca == "" {
		profile = "encrypted"
		args = append(args, "--local-plaintext")
	} else {
		args = append(args, "--ca", ca)
	}
	args = append(args, "--profile", profile)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = "../.."
	command.Stdin = strings.NewReader(credentials.token(t, core.Request{Version: 1, RequestID: "example", Operation: core.CapabilitiesGet}, profile == "encrypted"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("example client: %v %s", err, output)
	}
	var r adapter.FrameResponse
	if json.Unmarshal(output, &r) != nil || r.CorrelationID != "example" || r.Result == nil || r.Result.Capabilities == nil {
		t.Fatal("example lost complete discovery", string(output))
	}
}
