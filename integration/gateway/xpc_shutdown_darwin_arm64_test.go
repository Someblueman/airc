//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestXPCBridgeCancellationJoinsGatewayAndReleasesService(t *testing.T) {
	f := newXPCFixture(t, xpcCallerUID())
	if response, _ := xpcCall(t, f, core.Request{Version: 1, RequestID: "xpc-before-shutdown", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil {
		t.Fatalf("XPC pre-shutdown capabilities=%+v", response)
	}
	if err := f.process.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("stop Mach gateway for pending XPC call: %v", err)
	}
	stopped := true
	defer func() {
		if stopped {
			_ = f.process.command.Process.Signal(syscall.SIGCONT)
		}
	}()
	request := core.Request{Version: 1, RequestID: "xpc-pending-at-shutdown", Operation: core.CapabilitiesGet}
	command := exec.CommandContext(t.Context(), f.client)
	command.Stdin = bytes.NewReader(machFrameBytes(t, machFrame(t, f.machIPCFixture, request)))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start pending XPC client: %v", err)
	}
	clientDone := make(chan struct{})
	var clientErr error
	go func() { clientErr = command.Wait(); close(clientDone) }()
	defer func() {
		select {
		case <-clientDone:
		default:
			_ = command.Process.Kill()
			<-clientDone
		}
	}()
	select {
	case <-clientDone:
		t.Fatalf("XPC client completed while gateway was stopped: err=%v stdout=%q stderr=%q", clientErr, stdout.String(), stderr.String())
	case <-time.After(300 * time.Millisecond):
	}
	servicePID := waitXPCServicePID(t, f.app, 3*time.Second)
	if err := f.process.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("resume Mach gateway to handle interrupt: %v", err)
	}
	stopped = false
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt Mach gateway with pending XPC request: %v", err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("Mach gateway shutdown: %v stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		status, _ := exec.Command("ps", "-p", strconv.Itoa(f.process.command.Process.Pid), "-o", "state=,command=").CombinedOutput()
		t.Fatalf("Mach gateway did not join after interrupt: status=%s stderr=%s", status, f.process.stderr.String())
	}
	select {
	case <-clientDone:
		if clientErr == nil {
			response := machOpenResponse(t, f.machIPCFixture, stdout.Bytes(), machFrame(t, f.machIPCFixture, request).CorrelationID)
			if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
				t.Fatalf("XPC response racing gateway shutdown=%+v", response)
			}
		} else if stdout.Len() != 0 {
			t.Fatalf("failed pending XPC client emitted a response: stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), clientErr)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("pending XPC client did not finish after gateway shutdown")
	}
	journal, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatalf("gateway shutdown retained journal ownership: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if records := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(records) != 0 {
		t.Fatalf("pending shutdown request reached native AIRC: %+v", records)
	}
	if servicePID <= 0 {
		t.Fatal("invalid observed XPC service PID")
	}
	waitXPCServiceExit(t, f.app, 8*time.Second)
}

func xpcServicePID(t *testing.T, app string) (int, bool) {
	t.Helper()
	serviceExecutable := app + "/Contents/XPCServices/org.airc.gateway.xpc.bridge.xpc/Contents/MacOS/bridge"
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		t.Fatalf("inspect XPC service process table: %v", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(strings.Join(fields[1:], " "), serviceExecutable) {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			t.Fatalf("parse XPC bridge PID from process table line %q: %v", line, err)
		}
		return pid, true
	}
	return 0, false
}

func waitXPCServicePID(t *testing.T, app string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid, ok := xpcServicePID(t, app); ok {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("embedded XPC bridge process for %s was not visible", app)
	return 0
}

func waitXPCServiceExit(t *testing.T, app string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, ok := xpcServicePID(t, app)
		if !ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("embedded XPC bridge process remained after its client disconnected")
}
