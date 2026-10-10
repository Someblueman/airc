//go:build darwin && arm64

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type dbusBridgeProcess struct {
	command    *exec.Cmd
	done       chan struct{}
	err        error
	stderr     bytes.Buffer
	ready      chan string
	stdoutDone chan struct{}
}

type dbusFixture struct {
	machIPCFixture
	root   string
	socket string
	server string
	client string
	bridge *dbusBridgeProcess
}

func newDBusFixture(t *testing.T, callerUID uint32, callerPrincipal string) dbusFixture {
	t.Helper()
	skipIfDBusGLibUnavailable(t)
	f := newMachIPCFixture(t, callerUID, callerPrincipal)
	root, err := os.MkdirTemp("/private/tmp", "airc-dbus-")
	if err != nil {
		t.Fatalf("create short private D-Bus fixture directory: %v", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		_ = os.RemoveAll(root)
		t.Fatalf("protect D-Bus fixture directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove D-Bus fixture directory: %v", err)
		}
	})
	output := filepath.Join(t.TempDir(), "dbus-bin")
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "build-dbus-bridge.sh"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, script, "--output", output, "--sanitize")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native D-Bus bridge: %v: %s", err, data)
	}
	server, client := filepath.Join(output, "dbus-server"), filepath.Join(output, "dbus-client")
	for _, executable := range []string{server, client} {
		info, err := os.Stat(executable)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			t.Fatalf("D-Bus bridge executable missing or not executable: %s stat=%v info=%v", executable, err, info)
		}
	}
	socket := filepath.Join(root, "gateway.sock")
	bridge := startDBusBridge(t, server, socket, f.serviceName)
	return dbusFixture{machIPCFixture: f, root: root, socket: socket, server: server, client: client, bridge: bridge}
}

func skipIfDBusGLibUnavailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pkg-config"); err != nil {
		t.Skipf("D-Bus native tests need pkg-config and GLib GIO development files: %v", err)
	}
	if output, err := exec.Command("pkg-config", "--modversion", "gio-2.0", "gio-unix-2.0").CombinedOutput(); err != nil {
		t.Skipf("D-Bus native tests need GLib gio-2.0 and gio-unix-2.0 development files: %v: %s", err, output)
	}
}

func startDBusBridge(t *testing.T, server, socket, machService string) *dbusBridgeProcess {
	t.Helper()
	command := exec.Command(server, "--socket", socket, "--mach-service", machService)
	command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &dbusBridgeProcess{command: command, done: make(chan struct{}), ready: make(chan string, 1), stdoutDone: make(chan struct{})}
	command.Stderr = &p.stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start D-Bus bridge: %v", err)
	}
	t.Logf("owned D-Bus bridge pid=%d", command.Process.Pid)
	go func() {
		defer close(p.stdoutDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case p.ready <- scanner.Text():
			default:
			}
		}
	}()
	go func() { p.err = command.Wait(); <-p.stdoutDone; close(p.done) }()
	select {
	case line := <-p.ready:
		if line != "AIRC_DBUS_READY" {
			stopDBusBridge(t, p)
			t.Fatalf("unexpected D-Bus readiness line %q", line)
		}
	case <-p.done:
		t.Fatalf("D-Bus bridge exited before readiness: %v stderr=%s", p.err, p.stderr.String())
	case <-time.After(5 * time.Second):
		stopDBusBridge(t, p)
		t.Fatalf("D-Bus bridge did not report readiness; stderr=%s", p.stderr.String())
	}
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		stopDBusBridge(t, p)
		t.Fatalf("D-Bus peer socket not protected and ready: mode=%v err=%v", info, err)
	}
	t.Cleanup(func() {
		stopDBusBridge(t, p)
		if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("D-Bus bridge left its socket after shutdown: stat err=%v", err)
		}
		if p.err != nil {
			t.Errorf("D-Bus bridge exited: %v stderr=%s", p.err, p.stderr.String())
		}
		if p.stderr.Len() != 0 {
			t.Errorf("D-Bus bridge emitted diagnostics: %s", p.stderr.String())
		}
	})
	return p
}

func stopDBusBridge(t *testing.T, p *dbusBridgeProcess) {
	t.Helper()
	select {
	case <-p.done:
		return
	default:
	}
	if err := p.command.Process.Signal(os.Interrupt); err != nil {
		select {
		case <-p.done:
			return
		default:
			t.Errorf("interrupt D-Bus bridge: %v", err)
		}
	}
	select {
	case <-p.done:
	case <-time.After(6 * time.Second):
		if err := p.command.Process.Signal(os.Kill); err != nil {
			t.Errorf("kill owned D-Bus bridge after SIGINT timeout: %v", err)
		}
		joined := false
		select {
		case <-p.done:
			joined = true
		case <-time.After(2 * time.Second):
			t.Errorf("D-Bus bridge did not join after kill")
		}
		if joined {
			t.Errorf("D-Bus bridge did not join after SIGINT: stderr=%s", p.stderr.String())
		} else {
			t.Errorf("D-Bus bridge did not join after SIGINT or kill")
		}
	}
}

func dbusCall(t *testing.T, f dbusFixture, request core.Request) (adapter.FrameResponse, int) {
	t.Helper()
	return dbusCallFrame(t, f, machFrame(t, f.machIPCFixture, request))
}

func dbusCallFrame(t *testing.T, f dbusFixture, frame adapter.FrameRequest) (adapter.FrameResponse, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	wire, err := runDBusClient(ctx, f.client, f.socket, machFrameBytes(t, frame), false)
	if err != nil {
		t.Fatalf("D-Bus request %q: %v", frame.CorrelationID, err)
	}
	return machOpenResponse(t, f.machIPCFixture, wire, frame.CorrelationID), len(wire)
}

func dbusNoReply(t *testing.T, f dbusFixture, frame adapter.FrameRequest) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	wire, err := runDBusClient(ctx, f.client, f.socket, machFrameBytes(t, frame), false)
	if err == nil || len(wire) != 0 {
		t.Fatalf("refused D-Bus request returned response: bytes=%d err=%v", len(wire), err)
	}
}

func runDBusClient(ctx context.Context, client, socket string, frame []byte, drop bool) ([]byte, error) {
	args := []string{"--socket", socket}
	if drop {
		args = append(args, "--drop-reply")
	}
	command := exec.CommandContext(ctx, client, args...)
	command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	command.Stdin = bytes.NewReader(frame)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("native D-Bus client: %w: %s", err, stderr.String())
	}
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("native D-Bus client emitted diagnostics: %s", stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}
