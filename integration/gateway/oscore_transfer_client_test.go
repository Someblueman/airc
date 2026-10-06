//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func oscoreTransferCommand(t *testing.T, f *oscoreFixture, request core.Request, address string, sequence uint64) *exec.Cmd {
	t.Helper()
	root := t.TempDir()
	input, seq := filepath.Join(root, "request.json"), filepath.Join(root, "peer.seq")
	data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "signed", Envelope: f.credentials.token(t, request, false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 1024 {
		t.Fatal("transfer control requires a single request block")
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seq, []byte(strconv.FormatUint(sequence, 10)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, f.client, "-v", "0", "-B", "10", "-m", "post", "-t", "50", "-b", "1024", "-L", "3", "-f", input, "-E", f.peer+","+seq, "-o", filepath.Join(root, "response.json"), "coap://"+address+"/v1/coap/rpc")
	return command
}

func startHeldOSCOREClient(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("held native client did not join")
		}
	})
}

func oscoreResponseTime(t *testing.T, relay *oscoreCapture) time.Time {
	t.Helper()
	select {
	case at := <-relay.responses:
		return at
	case <-time.After(2 * time.Second):
		t.Fatal("protected response missing")
	}
	return time.Time{}
}
