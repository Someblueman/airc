package gateway_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

const baseline = "96f16c3bbb8a3faac2e69684f591af63ceb58450"
const archiveSHA256 = "4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829"

// fixture uses a checksum-verified pinned executable and owns its child process,
// socket and all state. It never uses an installed daemon or its defaults.
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
	root, err := os.MkdirTemp("/tmp", "agw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	binary := pinnedFixtureBinary(t)
	socket := filepath.Join(root, "irc.sock")
	log, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	args := []string{"--unix", socket, "--history", "32", "--history-file", filepath.Join(root, "history.jsonl"), "--sync", syncMode, "--log-format", "json"}
	if administration {
		adminFile := filepath.Join(root, "admin.token")
		if err := auth.CreateCredential(adminFile); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--admin-token-file", adminFile)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"HOME=" + root, "XDG_CONFIG_HOME=" + root, "AIRC_STATE_DIR=" + root}
	cmd.Stdout = log
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture baseline=%s archive_sha256=%s pid=%d", baseline, archiveSHA256, cmd.Process.Pid)
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
