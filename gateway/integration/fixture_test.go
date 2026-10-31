package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/internal/testfs"
)

// aircdFixture is aircd built once per test binary from this tree. Only the
// executable is shared: every daemon owns its state, socket and lifetime.
var aircdFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func TestMain(m *testing.M) {
	code := m.Run()
	for _, root := range []string{aircdFixture.root, currentGatewayFixture.root, framedClientFixture.root} {
		if root != "" {
			if err := os.RemoveAll(root); err != nil {
				fmt.Fprintln(os.Stderr, "cleaning fixture:", err)
				code = 1
			}
		}
	}
	os.Exit(code)
}

func aircdBinary(t *testing.T) string {
	t.Helper()
	aircdFixture.once.Do(func() {
		root, err := testfs.MkdirTemp("agw-aircd-")
		if err != nil {
			aircdFixture.err = err
			return
		}
		aircdFixture.root = root
		binary := filepath.Join(root, "aircd")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "github.com/Someblueman/airc/cmd/aircd")
		build.Dir = ".."
		if output, err := build.CombinedOutput(); err != nil {
			aircdFixture.err = fmt.Errorf("building aircd: %w\n%s", err, output)
			return
		}
		aircdFixture.binary = binary
	})
	if aircdFixture.err != nil {
		t.Fatal(aircdFixture.err)
	}
	return aircdFixture.binary
}

// fixture starts an aircd that owns its child process, socket and all state.
func fixture(t *testing.T) string {
	return fixtureWithAdmin(t, false)
}

// Administration is enabled only for tests that exercise native ownership
// boundaries, using a fresh owner-only credential inside the disposable root.
func fixtureWithAdmin(t *testing.T, administration bool) string {
	t.Helper()
	return nativeFixture(t, administration, "fsync")
}

func nativeFixture(t *testing.T, administration bool, syncMode string) string {
	t.Helper()
	return startNative(t, fixtureRoot(t), administration, syncMode)
}

// currentNative starts an aircd that trusts a fresh gateway credential for
// WEBIRC. It returns the daemon's socket and the credential file the gateway
// upstream names.
func currentNative(t *testing.T) (socket, gatewayToken string) {
	t.Helper()
	root := fixtureRoot(t)
	gatewayToken = filepath.Join(root, "gateway.token")
	if err := auth.CreateCredential(gatewayToken); err != nil {
		t.Fatal(err)
	}
	return startNative(t, root, false, "none", "--gateway-token-file", gatewayToken), gatewayToken
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := testfs.MkdirTemp("agw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// memoryHistory holds the tests whose daemon keeps no history file, so a
// cursor pushed out of its 32-message window is a gap; with a history file
// the archive would resume it.
var memoryHistory sync.Map

func withoutHistoryFile(t *testing.T) {
	memoryHistory.Store(t.Name(), true)
	t.Cleanup(func() { memoryHistory.Delete(t.Name()) })
}

// startNative runs aircd on a Unix socket in root, which the caller owns, with
// any extra flags, and stops it when the test ends.
func startNative(t *testing.T, root string, administration bool, syncMode string, extra ...string) string {
	t.Helper()
	socket := filepath.Join(root, "irc.sock")
	log, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	args := []string{"--unix", socket, "--history", "32", "--history-file", filepath.Join(root, "history.jsonl"), "--sync", syncMode, "--log-format", "json"}
	if _, memory := memoryHistory.Load(t.Name()); memory {
		// Drop --history-file and its path. Accounts then sit beside the
		// admin credential, the only other file they derive from, so these
		// daemons administer.
		args = slices.Delete(args, 4, 6)
		administration = true
	}
	if administration {
		adminFile := filepath.Join(root, "admin.token")
		if err := auth.CreateCredential(adminFile); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--admin-token-file", adminFile)
	}
	args = append(args, extra...)
	cmd := exec.Command(aircdBinary(t), args...)
	cmd.Env = []string{"HOME=" + root, "XDG_CONFIG_HOME=" + root, "AIRC_STATE_DIR=" + root}
	cmd.Stdout = log
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture aircd pid=%d", cmd.Process.Pid)
	ready := make(chan struct{}, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Bytes()
			_, _ = log.Write(append(append([]byte(nil), line...), '\n'))
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal(line, &event) == nil && event.Message == "server_started" && event.Address == socket {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()
	done := make(chan struct{})
	var waitErr error
	go func() { <-readDone; waitErr = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
			if waitErr != nil {
				t.Errorf("fixture shutdown: %v", waitErr)
			}
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("fixture required forced termination")
		}
		if c, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
			_ = c.Close()
			t.Error("fixture listener survived shutdown")
		}
	})
	select {
	case <-ready:
		c, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("fixture announced ready but listener is unusable: %v", err)
		}
		_ = c.Close()
		return socket
	case <-done:
		output, _ := os.ReadFile(log.Name())
		t.Fatalf("fixture exited before ready: %v: %s", waitErr, output)
	case <-time.After(5 * time.Second):
		t.Fatalf("fixture failed to announce ready within 5 seconds; log: %s", log.Name())
	}
	return ""
}
