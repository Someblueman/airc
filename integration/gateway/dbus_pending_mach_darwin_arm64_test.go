//go:build darwin && arm64

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

type pendingMachProbe struct {
	command    *exec.Cmd
	stdin      io.WriteCloser
	done       chan struct{}
	lines      chan string
	stdoutDone chan struct{}
	stderr     bytes.Buffer
	err        error
}

func startPendingMachProbe(t *testing.T, serviceName string) *pendingMachProbe {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	compiler, err := exec.CommandContext(ctx, "xcrun", "--sdk", "macosx", "--find", "clang").Output()
	if err != nil {
		t.Fatalf("find Apple Clang for pending Mach probe: %v", err)
	}
	sdk, err := exec.CommandContext(ctx, "xcrun", "--sdk", "macosx", "--show-sdk-path").Output()
	if err != nil {
		t.Fatalf("find macOS SDK for pending Mach probe: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "dbus-pending-mach")
	source := filepath.Join("testdata", "dbus_pending_mach.c")
	command := exec.CommandContext(ctx, string(bytes.TrimSpace(compiler)), "-std=c11", "-Wall", "-Wextra", "-Werror", "-pedantic", "-arch", "arm64", "-isysroot", string(bytes.TrimSpace(sdk)), "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-omit-frame-pointer", source, "-o", binary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build pending Mach reply probe: %v: %s", err, output)
	}
	probe := &pendingMachProbe{
		command:    exec.Command(binary, serviceName),
		done:       make(chan struct{}),
		lines:      make(chan string, 2),
		stdoutDone: make(chan struct{}),
	}
	stdout, err := probe.command.StdoutPipe()
	if err != nil {
		t.Fatalf("capture pending Mach probe markers: %v", err)
	}
	stdin, err := probe.command.StdinPipe()
	if err != nil {
		t.Fatalf("control pending Mach probe lifetime: %v", err)
	}
	probe.stdin = stdin
	probe.command.Stderr = &probe.stderr
	if err := probe.command.Start(); err != nil {
		t.Fatalf("start pending Mach probe: %v", err)
	}
	go func() {
		defer close(probe.stdoutDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			probe.lines <- scanner.Text()
		}
	}()
	go func() {
		probe.err = probe.command.Wait()
		<-probe.stdoutDone
		close(probe.done)
	}()
	t.Cleanup(func() {
		_ = probe.stdin.Close()
		joined := false
		select {
		case <-probe.done:
			joined = true
		case <-time.After(2 * time.Second):
			_ = probe.command.Process.Kill()
			select {
			case <-probe.done:
				joined = true
				t.Errorf("pending Mach probe required forced termination: %v stderr=%s", probe.err, probe.stderr.String())
			case <-time.After(2 * time.Second):
				t.Errorf("pending Mach probe failed to join after kill")
			}
		}
		if !joined {
			return
		}
		if probe.err != nil {
			t.Errorf("pending Mach probe exited: %v stderr=%s", probe.err, probe.stderr.String())
		}
		if probe.stderr.Len() != 0 {
			t.Errorf("pending Mach probe emitted diagnostics: %s", probe.stderr.String())
		}
	})
	if got := waitPendingMachLine(t, probe, 5*time.Second); got != "READY" {
		t.Fatalf("pending Mach probe readiness=%q want READY", got)
	}
	return probe
}

func waitPendingMachLine(t *testing.T, probe *pendingMachProbe, timeout time.Duration) string {
	t.Helper()
	select {
	case line := <-probe.lines:
		return line
	case <-probe.done:
		t.Fatalf("pending Mach probe exited before marker: %v stderr=%s", probe.err, probe.stderr.String())
	case <-time.After(timeout):
		t.Fatalf("pending Mach probe marker timed out after %s", timeout)
	}
	return ""
}

func TestDBusPeerDisconnectDuringPendingMachCallJoinsShutdown(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	stopDBusBridge(t, f.bridge)
	if f.bridge.err != nil || f.bridge.stderr.Len() != 0 {
		t.Fatalf("initial D-Bus bridge shutdown: err=%v stderr=%s", f.bridge.err, f.bridge.stderr.String())
	}
	if _, err := os.Lstat(f.socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initial D-Bus socket remained before controlled upstream: %v", err)
	}
	serviceName := fmt.Sprintf("com.airc.gateway.pending.%d.%d", os.Getpid(), time.Now().UnixNano())
	probe := startPendingMachProbe(t, serviceName)
	f.bridge = startDBusBridge(t, f.server, f.socket, serviceName)
	request := core.Request{Version: 1, RequestID: "dbus-pending-mach-disconnect", Operation: core.CapabilitiesGet}
	frame := machFrame(t, f.machIPCFixture, request)
	client := exec.Command(f.client, "--socket", f.socket)
	client.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	client.Stdin = bytes.NewReader(machFrameBytes(t, frame))
	var clientOut, clientErr bytes.Buffer
	client.Stdout, client.Stderr = &clientOut, &clientErr
	if err := client.Start(); err != nil {
		t.Fatalf("start D-Bus client for held Mach reply: %v", err)
	}
	clientDone := make(chan error, 1)
	go func() { clientDone <- client.Wait() }()
	clientJoined := false
	clientExpectedStop := false
	t.Cleanup(func() {
		if clientJoined {
			return
		}
		_ = client.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-clientDone:
			clientJoined = true
			if !clientExpectedStop && err == nil {
				t.Errorf("D-Bus client completed unexpectedly during test cleanup")
			}
		case <-time.After(2 * time.Second):
			_ = client.Process.Kill()
			select {
			case <-clientDone:
				clientJoined = true
				t.Errorf("D-Bus client required forced termination during cleanup")
			case <-time.After(2 * time.Second):
				t.Errorf("D-Bus client failed to join after kill")
			}
		}
	})
	if marker := waitPendingMachLine(t, probe, 6*time.Second); marker != "RECEIVED" {
		t.Fatalf("pending Mach probe receipt marker=%q want RECEIVED", marker)
	}
	if err := client.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("close D-Bus client while Mach reply is held: %v", err)
	}
	clientExpectedStop = true
	select {
	case err := <-clientDone:
		clientJoined = true
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("D-Bus client exit after peer cancellation: err=%v stdout=%q stderr=%s", err, clientOut.Bytes(), clientErr.String())
		}
	case <-time.After(2 * time.Second):
		_ = client.Process.Kill()
		select {
		case err := <-clientDone:
			t.Fatalf("D-Bus client did not respond to SIGTERM: joined err=%v", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("D-Bus client failed to join after kill")
		}
	}

	started := time.Now()
	if err := f.bridge.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop bridge while its Mach worker is waiting for a reply: %v", err)
	}
	select {
	case <-f.bridge.done:
		elapsed := time.Since(started)
		if f.bridge.err != nil || elapsed < time.Second || elapsed > 6*time.Second {
			t.Fatalf("bridge did not join after bounded Mach receive: elapsed=%s err=%v stderr=%s", elapsed, f.bridge.err, f.bridge.stderr.String())
		}
		if f.bridge.stderr.Len() != 0 {
			t.Fatalf("bridge emitted diagnostics during pending-Mach shutdown: %s", f.bridge.stderr.String())
		}
	case <-time.After(6 * time.Second):
		stopDBusBridge(t, f.bridge)
		t.Fatal("bridge did not join after pending Mach call")
	}
	if _, err := os.Lstat(f.socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bridge left D-Bus socket after pending-Mach shutdown: %v", err)
	}
	if err := probe.stdin.Close(); err != nil {
		t.Fatalf("release pending Mach reply probe: %v", err)
	}
	select {
	case <-probe.done:
		if probe.err != nil || probe.stderr.Len() != 0 {
			t.Fatalf("pending Mach probe cleanup err=%v stderr=%s", probe.err, probe.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("pending Mach probe failed to join after release")
	}
}
