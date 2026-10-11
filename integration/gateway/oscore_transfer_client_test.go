//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func oscoreTransferCommand(t *testing.T, f *oscoreFixture, request core.Request, address string, sequence uint64) *exec.Cmd {
	t.Helper()
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 1024 {
		t.Fatal("transfer control requires a single request block")
	}
	command, _, _ := oscoreNativeCommand(t, f, data, address, sequence, 1024)
	return command
}

func oscoreNativeCommand(t *testing.T, f *oscoreFixture, data []byte, address string, sequence uint64, blockSize int) (*exec.Cmd, string, string) {
	t.Helper()
	if len(data) == 0 || len(data) > 65536 {
		t.Fatal("native fixture input outside bounds")
	}
	root := t.TempDir()
	input, seq, output := filepath.Join(root, "request.json"), filepath.Join(root, "peer.seq"), filepath.Join(root, "response.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seq, []byte(strconv.FormatUint(sequence, 10)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, f.client, "-v", "0", "-B", "10", "-m", "post", "-t", "50", "-b", strconv.Itoa(blockSize), "-L", "3", "-f", input, "-E", f.peer+","+seq, "-o", output, "coap://"+address+"/v1/coap/rpc")
	return command, seq, output
}

func startHeldOSCOREClient(t *testing.T, command *exec.Cmd) func() {
	t.Helper()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = command.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("held native client did not join")
			}
		})
	}
	t.Cleanup(stop)
	return stop
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
