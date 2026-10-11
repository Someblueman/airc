//go:build darwin && arm64

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/machipc"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// A pre-filled shared slot proves refusal before journal intake without timing a
// competing backend request. The client still crosses the real Mach boundary.
func TestMachIPCNativeCapacityBeforeIntakeAndRetry(t *testing.T) {
	f, request := sctpApplicationFixture(t)
	requests := make(chan struct{}, 1)
	requests <- struct{}{}
	driver, err := NewMachIPCAuthor(f.d, requests, []uint32{uint32(os.Geteuid())})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("org.airc.capacity.%d.%d", os.Getpid(), time.Now().UnixNano())
	listener, err := machipc.Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- driver.Serve(ctx, listener) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("Mach capacity fixture did not join")
			}
		}
	})
	client := filepath.Join(t.TempDir(), "mach-client")
	buildCtx, finish := context.WithTimeout(t.Context(), 20*time.Second)
	build := exec.CommandContext(buildCtx, "clang", "-std=c11", "-Wall", "-Wextra", "-Werror", "-pedantic", "-fsanitize=address,undefined", "-g",
		filepath.Join("..", "..", "..", "integration", "gateway", "testdata", "mach_ipc_client.c"), "-o", client)
	output, err := build.CombinedOutput()
	finish()
	if err != nil {
		t.Fatalf("compile native client: %v: %s", err, output)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	call := func() FrameResponse {
		t.Helper()
		callCtx, finish := context.WithTimeout(t.Context(), 5*time.Second)
		defer finish()
		cmd := exec.CommandContext(callCtx, client, name)
		cmd.Stdin = bytes.NewReader(body)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		reply, err := cmd.Output()
		if err != nil {
			t.Fatalf("native Mach call: %v: %s", err, stderr.String())
		}
		var response FrameResponse
		if err := json.Unmarshal(reply, &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := call()
	if response.Event != "error" || response.Error == nil || response.Error.Code != core.CapacityExceeded || !response.Error.Transient || response.Sequence != 1 || response.CorrelationID != request.CorrelationID {
		t.Fatalf("capacity response was not correlated: %+v", response)
	}
	if _, err := f.lookup(t, mqttSNRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("capacity request entered journal: %v", err)
	}
	<-requests
	response = call()
	if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != mqttSNRequest().RequestID {
		t.Fatalf("retained request did not enter journal after capacity recovered: %+v", response)
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Mach capacity fixture did not join after cancellation")
	}
	if len(requests) != 0 {
		t.Fatal("joined Mach request retained global capacity")
	}
}
