//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package gateway_test

import (
	"context"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSystemVQueueInputAndResponsePressureRemainBounded(t *testing.T) {
	f := newSystemVFixture(t)
	if err := f.process.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.process.command.Process.Signal(syscall.SIGCONT) })
	waitSystemVGatewayStopped(t, f.process.command.Process.Pid)
	fillSystemVQueue(t, f.source)
	if err := f.process.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	systemVCall(t, f, core.Request{Version: 1, RequestID: "sv-input-pressure-fence", Operation: core.CapabilitiesGet})
	rpcNoJobs(t, f.config.StateDir)
	filled := fillSystemVQueue(t, f.mailbox)
	request := core.Request{Version: 1, RequestID: "sv-blocked-response", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "System V pressure processing witness"}
	writeSystemVFragments(t, f, systemVFragments(t, systemVFrame(t, f, request, false)))
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == request.Message })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name {
		t.Fatal("native processing witness mismatch", seen)
	}
	// The configured protected writer has a three-second deadline. Drain only
	// after that bound so the blocked response cannot become a successful write.
	time.Sleep(3500 * time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	buf := make([]byte, 1000)
	for range filled {
		n, err := f.mailbox.ReadMessage(ctx, buf)
		if err != nil || n != len(systemVPressureFiller) || string(buf[:n]) != string(systemVPressureFiller) {
			t.Fatal("pressure filler changed", n, err)
		}
	}
	assertSystemVSilent(t, f.mailbox)
	systemVCall(t, f, core.Request{Version: 1, RequestID: "sv-response-pressure-recovery", Operation: core.CapabilitiesGet})
	receipt := pollSystemVReceipt(t, f, request.RequestID)
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID != seen.ID {
		t.Fatal("pressure receipt mismatch", receipt)
	}
}

var systemVPressureFiller = []byte(strings.Repeat("x", 1000))

func fillSystemVQueue(t *testing.T, q *sysvqueue.Queue) int {
	t.Helper()
	for count := 0; count < 66; count++ {
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		err := q.WriteMessage(ctx, systemVPressureFiller)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			if count == 0 {
				t.Fatal("queue held no whole filler")
			}
			return count
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("queue exceeded configured maximum capacity")
	return 0
}
