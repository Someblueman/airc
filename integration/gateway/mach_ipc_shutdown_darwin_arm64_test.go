//go:build darwin && arm64

package gateway_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestMachIPCShutdownReleasesJournalAndBootstrapLookup(t *testing.T) {
	f := newMachIPCFixture(t, uint32(os.Geteuid()), "device")
	if response, _ := machCall(t, f, core.Request{Version: 1, RequestID: "mach-before-shutdown", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil {
		t.Fatalf("Mach IPC pre-shutdown capabilities=%+v", response)
	}
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt Mach IPC gateway: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("Mach IPC gateway shutdown: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Mach IPC gateway did not join after shutdown")
	}
	journal, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatalf("Mach IPC shutdown retained journal ownership: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	frame := machFrame(t, f, core.Request{Version: 1, RequestID: "mach-after-shutdown", Operation: core.CapabilitiesGet})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	wire, err := runMachIPCClient(ctx, f.client, f.serviceName, machFrameBytes(t, frame))
	if err == nil || len(wire) != 0 || !strings.Contains(err.Error(), "bootstrap_look_up failed") {
		t.Fatalf("bootstrap name remained available after gateway shutdown: bytes=%d err=%v", len(wire), err)
	}
}
