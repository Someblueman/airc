//go:build darwin && arm64

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func buildDBusProbe(t *testing.T) string {
	t.Helper()
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "gio-2.0", "gio-unix-2.0").Output()
	if err != nil {
		t.Fatalf("query GLib gio-2.0 compiler flags: %v", err)
	}
	output := filepath.Join(t.TempDir(), "dbus-invalid-call")
	args := []string{"-std=c11", "-Wall", "-Wextra", "-Werror", "-pedantic", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-omit-frame-pointer", "testdata/dbus_invalid_call.c", "-o", output}
	args = append(args, strings.Fields(string(flags))...)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "clang", args...)
	if diagnostic, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build independent D-Bus SDK probe: %v: %s", err, diagnostic)
	}
	if info, err := os.Stat(output); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("D-Bus SDK probe missing or not executable: info=%v err=%v", info, err)
	}
	return output
}

func runDBusProbe(ctx context.Context, probe, socket, mode string) ([]byte, error) {
	command := exec.CommandContext(ctx, probe, socket, mode)
	command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("D-Bus SDK probe %s: %w: %s", mode, err, stderr.String())
	}
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("D-Bus SDK probe %s emitted diagnostics: %s", mode, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func dbusFrameForEnvelope(envelope string) adapter.FrameRequest {
	digest := sha256.Sum256([]byte(envelope))
	return adapter.FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
}

func TestDBusBridgeRejectsBadAuthFramesAndPeerCallsBeforeIntake(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	base := core.Request{Version: 1, RequestID: "dbus-refusal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"}
	cases := []struct {
		name  string
		frame func() adapter.FrameRequest
	}{
		{name: "invalid-authentication", frame: func() adapter.FrameRequest { return dbusFrameForEnvelope("invalid compact token") }},
		{name: "signed-profile-refused", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f.machIPCFixture, base)
			frame.Profile = "signed"
			return frame
		}},
		{name: "correlation-mismatch", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f.machIPCFixture, base)
			frame.CorrelationID = strings.Repeat("0", 64)
			return frame
		}},
		{name: "stream-refused", frame: func() adapter.FrameRequest {
			frame := machFrame(t, f.machIPCFixture, base)
			frame.Stream = true
			return frame
		}},
		{name: "expired-envelope", frame: func() adapter.FrameRequest {
			now := time.Now().Unix()
			return dbusFrameForEnvelope(f.credentials.validityToken(t, base, now-600, now-300, true))
		}},
		{name: "wrong-audience", frame: func() adapter.FrameRequest {
			now := time.Now().Unix()
			realm := f.credentials.realm
			f.credentials.realm = "different-realm"
			token := f.credentials.validityToken(t, base, now, now+120, true)
			f.credentials.realm = realm
			return dbusFrameForEnvelope(token)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbusNoReply(t, f, tc.frame())
			machAssertNoIntake(t, f.machIPCFixture)
		})
	}
	oversized := append(machFrameBytes(t, machFrame(t, f.machIPCFixture, base)), bytes.Repeat([]byte(" "), (16<<10)+1)...)
	oversized = oversized[:(16<<10)+1]
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if output, err := runDBusClient(ctx, f.client, f.socket, oversized, false); err == nil || len(output) != 0 {
		t.Fatalf("D-Bus CLI accepted oversized frame: bytes=%d err=%v", len(output), err)
	}
	machAssertNoIntake(t, f.machIPCFixture)
	probe := buildDBusProbe(t)
	for _, mode := range []string{"wrong-signature", "missing-argument", "extra-argument", "oversized-argument", "fd-transfer"} {
		t.Run("sdk-"+mode, func(t *testing.T) {
			probeCtx, probeCancel := context.WithTimeout(t.Context(), 7*time.Second)
			defer probeCancel()
			output, err := runDBusProbe(probeCtx, probe, f.socket, mode)
			if err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) && exit.ExitCode() == 77 && mode == "fd-transfer" {
					t.Skip("peer D-Bus implementation did not negotiate Unix FD passing")
				}
				t.Fatalf("direct D-Bus SDK %s response=%q err=%v", mode, output, err)
			}
			if string(output) != "rejected\n" {
				t.Fatalf("direct D-Bus SDK %s output=%q", mode, output)
			}
			machAssertNoIntake(t, f.machIPCFixture)
		})
	}
	valid := core.Request{Version: 1, RequestID: "dbus-after-refusal", Operation: core.Send, Target: base.Target, Message: "bridge remains usable after malformed calls"}
	response, _ := dbusCall(t, f, valid)
	if response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("valid D-Bus request after refusal not admitted: %+v", response)
	}
	receipt := machAwaitAccepted(t, f.machIPCFixture, valid.RequestID)
	if receipt.SendAttempts != 1 {
		t.Fatalf("post-refusal receipt=%+v", receipt)
	}
	seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != base.Target.Name || seen.Message != valid.Message {
		t.Fatalf("post-refusal native event=%+v", seen)
	}
}

func TestDBusBridgeUnauthenticatedPeerTimesOutAndRecovers(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	stalled, err := net.DialTimeout("unix", f.socket, time.Second)
	if err != nil {
		t.Fatalf("open stalled unauthenticated peer: %v", err)
	}
	defer stalled.Close()
	if _, err := stalled.Write([]byte{0, 'A', 'U', 'T', 'H'}); err != nil {
		t.Fatalf("start and hold partial D-Bus EXTERNAL authentication: %v", err)
	}
	started := time.Now()
	var response *adapter.FrameResponse
	for attempt := 0; time.Since(started) < 8*time.Second; attempt++ {
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("dbus-after-stall-%d", attempt), Operation: core.CapabilitiesGet}
		frame := machFrame(t, f.machIPCFixture, request)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		wire, callErr := runDBusClient(ctx, f.client, f.socket, machFrameBytes(t, frame), false)
		cancel()
		if callErr == nil {
			opened := machOpenResponse(t, f.machIPCFixture, wire, frame.CorrelationID)
			response = &opened
			break
		}
		if time.Since(started) > 7*time.Second {
			t.Fatalf("valid call did not recover before the authentication bound: elapsed=%s err=%v", time.Since(started), callErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	elapsed := time.Since(started)
	if response == nil || response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil || elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("unauthenticated peer did not expire within bounded slot deadline: elapsed=%s response=%+v", elapsed, response)
	}
	if err := stalled.Close(); err != nil {
		t.Fatalf("close timed-out unauthenticated peer: %v", err)
	}
	second, _ := dbusCall(t, f, core.Request{Version: 1, RequestID: "dbus-recovered", Operation: core.CapabilitiesGet})
	if second.Event != "result" || second.Result == nil || second.Result.Capabilities == nil {
		t.Fatalf("D-Bus listener did not recover after stalled peer: %+v", second)
	}
}

func TestDBusBridgeAuthenticatedIdlePeerExpiresAndRecovers(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	peer, err := net.DialTimeout("unix", f.socket, time.Second)
	if err != nil {
		t.Fatalf("open raw D-Bus idle peer: %v", err)
	}
	defer peer.Close()
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("bound raw D-Bus authentication: %v", err)
	}
	identity := hex.EncodeToString([]byte(fmt.Sprint(os.Geteuid())))
	if _, err := fmt.Fprintf(peer, "\x00AUTH EXTERNAL %s\r\n", identity); err != nil {
		t.Fatalf("authenticate raw D-Bus peer: %v", err)
	}
	line, err := bufio.NewReaderSize(peer, 128).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "OK ") {
		t.Fatalf("D-Bus EXTERNAL authentication response=%q err=%v", line, err)
	}
	if _, err := peer.Write([]byte("BEGIN\r\n")); err != nil {
		t.Fatalf("begin authenticated D-Bus session: %v", err)
	}
	started := time.Now()
	if err := peer.SetReadDeadline(started.Add(36 * time.Second)); err != nil {
		t.Fatalf("bound authenticated idle wait: %v", err)
	}
	var extra [1]byte
	n, readErr := peer.Read(extra[:])
	elapsed := time.Since(started)
	if n != 0 || !errors.Is(readErr, io.EOF) || elapsed < 28*time.Second || elapsed > 36*time.Second {
		t.Fatalf("authenticated idle peer did not expire at its bound: bytes=%d err=%v elapsed=%s", n, readErr, elapsed)
	}
	if err := peer.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("clear expired peer deadlines: %v", err)
	}
	response, _ := dbusCall(t, f, core.Request{Version: 1, RequestID: "dbus-after-idle-expiry", Operation: core.CapabilitiesGet})
	if response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("D-Bus listener failed to recover after idle expiry: %+v", response)
	}
}

func TestDBusBridgeSIGTERMJoinsAndUnlinksOnlyItsSocket(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	if response, _ := dbusCall(t, f, core.Request{Version: 1, RequestID: "dbus-before-term", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil {
		t.Fatalf("D-Bus pre-shutdown capabilities=%+v", response)
	}
	if err := f.bridge.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM to D-Bus bridge: %v", err)
	}
	select {
	case <-f.bridge.done:
		if f.bridge.err != nil {
			t.Fatalf("D-Bus bridge SIGTERM exit: %v stderr=%s", f.bridge.err, f.bridge.stderr.String())
		}
	case <-time.After(5 * time.Second):
		stopDBusBridge(t, f.bridge)
		t.Fatal("D-Bus bridge did not join after SIGTERM")
	}
	if _, err := os.Lstat(f.socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("D-Bus bridge left its socket after SIGTERM: lstat err=%v", err)
	}
	if response, _ := machCall(t, f.machIPCFixture, core.Request{Version: 1, RequestID: "mach-after-dbus-term", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil {
		t.Fatalf("D-Bus shutdown affected its gateway: %+v", response)
	}
}

func TestDBusBridgeSIGTERMJoinsDuringIncompleteAuthentication(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	stalled, err := net.DialTimeout("unix", f.socket, time.Second)
	if err != nil {
		t.Fatalf("open incomplete D-Bus peer: %v", err)
	}
	defer stalled.Close()
	if err := stalled.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("bound D-Bus auth handshake: %v", err)
	}
	if _, err := stalled.Write([]byte("\x00AUTH ANONYMOUS\r\n")); err != nil {
		t.Fatalf("send unsupported initial D-Bus auth mechanism: %v", err)
	}
	line, err := bufio.NewReaderSize(stalled, 128).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "REJECTED EXTERNAL") {
		t.Fatalf("D-Bus server did not reject unsupported mechanism and advertise EXTERNAL: line=%q err=%v", line, err)
	}
	if _, err := stalled.Write([]byte("AUTH EXTERNAL ")); err != nil {
		t.Fatalf("begin and stall an EXTERNAL auth command: %v", err)
	}
	if err := stalled.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("clear auth handshake deadline: %v", err)
	}
	if err := f.bridge.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM with authentication pending: %v", err)
	}
	select {
	case <-f.bridge.done:
		if f.bridge.err != nil {
			t.Fatalf("D-Bus bridge exit with authentication pending: %v stderr=%s", f.bridge.err, f.bridge.stderr.String())
		}
	case <-time.After(6 * time.Second):
		stopDBusBridge(t, f.bridge)
		t.Fatal("D-Bus bridge did not join with authentication pending")
	}
	if f.bridge.stderr.Len() != 0 {
		t.Fatalf("D-Bus bridge emitted diagnostics during pending-auth shutdown: %s", f.bridge.stderr.String())
	}
	if _, err := os.Lstat(f.socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("D-Bus bridge left socket after pending-auth shutdown: lstat err=%v", err)
	}
	if response, _ := machCall(t, f.machIPCFixture, core.Request{Version: 1, RequestID: "mach-after-dbus-pending-auth-term", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil {
		t.Fatalf("D-Bus pending-auth shutdown affected gateway: %+v", response)
	}
}

func TestDBusBridgeRejectsConfiguredUIDAndPrincipalMappingMismatch(t *testing.T) {
	uid := uint32(os.Geteuid())
	wrongUID := uid + 1
	if uid == ^uint32(0)-1 {
		wrongUID = uid - 1
	}
	t.Run("upstream-caller-uid", func(t *testing.T) {
		f := newDBusFixture(t, wrongUID, "device")
		frame := machFrame(t, f.machIPCFixture, core.Request{Version: 1, RequestID: "dbus-wrong-caller-uid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"})
		dbusNoReply(t, f, frame)
		machAssertNoIntake(t, f.machIPCFixture)
	})
	t.Run("upstream-principal", func(t *testing.T) {
		f := newDBusFixture(t, uid, "alternate")
		frame := machFrame(t, f.machIPCFixture, core.Request{Version: 1, RequestID: "dbus-wrong-caller-principal", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not enter journal"})
		dbusNoReply(t, f, frame)
		machAssertNoIntake(t, f.machIPCFixture)
	})
}

func TestDBusBridgeRefusesExistingSocketAndSymlink(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	before, err := os.Lstat(f.socket)
	if err != nil {
		t.Fatalf("stat active D-Bus socket: %v", err)
	}
	if output, err := runDBusRejectedStart(t, f, f.socket); err == nil || !strings.Contains(output, "socket path already exists") {
		t.Fatalf("second bridge start on owned socket was not refused: output=%q err=%v", output, err)
	}
	after, err := os.Lstat(f.socket)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("existing D-Bus socket changed after refusal: same=%v err=%v", err == nil && os.SameFile(before, after), err)
	}
	target := filepath.Join(f.root, "symlink-target")
	link := filepath.Join(f.root, "symlink.sock")
	const marker = "preserve this target"
	if err := os.WriteFile(target, []byte(marker), 0600); err != nil {
		t.Fatalf("create symlink target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create pre-existing socket symlink: %v", err)
	}
	if output, err := runDBusRejectedStart(t, f, link); err == nil || !strings.Contains(output, "socket path already exists") {
		t.Fatalf("bridge start on symlink was not refused: output=%q err=%v", output, err)
	}
	linkInfo, err := os.Lstat(link)
	linkTarget, readlinkErr := os.Readlink(link)
	contents, readErr := os.ReadFile(target)
	isSymlink := err == nil && linkInfo.Mode()&os.ModeSymlink != 0
	if !isSymlink || readlinkErr != nil || linkTarget != target || readErr != nil || string(contents) != marker {
		t.Fatalf("pre-existing symlink/target changed: info=%v target=%q bytes=%q lstat=%v readlink=%v read=%v", linkInfo, linkTarget, contents, err, readlinkErr, readErr)
	}
	if response, _ := dbusCall(t, f, core.Request{Version: 1, RequestID: "dbus-after-path-refusals", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil {
		t.Fatalf("active bridge failed after refused path collisions: %+v", response)
	}
}

func runDBusRejectedStart(t *testing.T, f dbusFixture, socket string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.server, "--socket", socket, "--mach-service", f.serviceName)
	command.Env = append(os.Environ(), "G_DEBUG=fatal-warnings")
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestDBusBridgeShutdownPreservesReplacedSocketPath(t *testing.T) {
	f := newDBusFixture(t, uint32(os.Geteuid()), "device")
	movedSocket := f.socket + ".original"
	if err := os.Rename(f.socket, movedSocket); err != nil {
		t.Fatalf("move owned socket path for replacement witness: %v", err)
	}
	const marker = "do not remove replacement"
	if err := os.WriteFile(f.socket, []byte(marker), 0600); err != nil {
		t.Fatalf("place replacement at former socket path: %v", err)
	}
	if err := f.bridge.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal bridge with replaced socket path: %v", err)
	}
	select {
	case <-f.bridge.done:
		if f.bridge.err != nil {
			t.Fatalf("bridge exit with replaced socket path: %v stderr=%s", f.bridge.err, f.bridge.stderr.String())
		}
	case <-time.After(6 * time.Second):
		stopDBusBridge(t, f.bridge)
		t.Fatal("bridge did not join with replaced socket path")
	}
	contents, err := os.ReadFile(f.socket)
	if err != nil || string(contents) != marker {
		t.Fatalf("bridge removed or altered replacement path: bytes=%q err=%v", contents, err)
	}
	if err := os.Remove(f.socket); err != nil {
		t.Fatalf("remove verified replacement file: %v", err)
	}
	if err := os.Remove(movedSocket); err != nil {
		t.Fatalf("remove moved original socket inode: %v", err)
	}
}
