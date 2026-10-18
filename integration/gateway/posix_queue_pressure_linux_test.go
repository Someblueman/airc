//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testmqueue"
)

func TestPOSIXQueueInputAndResponsePressureRemainBounded(t *testing.T) {
	testmqueue.Require(t)
	f := newPOSIXFixture(t)
	if err := f.process.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.process.command.Process.Signal(syscall.SIGCONT) })
	waitPOSIXGatewayStopped(t, f.process.command.Process.Pid)
	for range 8 {
		if err := f.source.WriteMessage(t.Context(), []byte("invalid whole fragment")); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	err := f.source.WriteMessage(ctx, []byte("ninth fragment"))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queue pressure did not respect caller bound", err)
	}
	if err := f.process.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	posixCall(t, f, core.Request{Version: 1, RequestID: "pq-input-pressure-fence", Operation: core.CapabilitiesGet})
	for range 8 {
		if err := f.mailbox.WriteMessage(t.Context(), []byte("response pressure filler")); err != nil {
			t.Fatal(err)
		}
	}
	request := core.Request{Version: 1, RequestID: "pq-blocked-response", Operation: core.CapabilitiesGet}
	writePOSIXFragments(t, f, posixFragments(t, posixFrame(t, f, request, false)))
	// The configured protected writer has a three-second deadline. Drain only
	// after that bound so the blocked response cannot become a successful write.
	time.Sleep(3500 * time.Millisecond)
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	buf := make([]byte, adapter.POSIXQueueMessageBytes)
	for range 8 {
		n, _, err := f.mailbox.ReadMessage(ctx, buf)
		if err != nil || string(buf[:n]) != "response pressure filler" {
			t.Fatal("pressure filler changed", n, err)
		}
	}
	assertPOSIXSilent(t, f.mailbox)
	posixCall(t, f, core.Request{Version: 1, RequestID: "pq-response-pressure-recovery", Operation: core.CapabilitiesGet})
	rpcNoJobs(t, f.config.StateDir)
}
