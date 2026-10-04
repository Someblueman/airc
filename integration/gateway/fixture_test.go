package gateway_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

const baseline = "96f16c3bbb8a3faac2e69684f591af63ceb58450"
const archiveSHA256 = "4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829"

// fixture exports an immutable source revision and owns its child process,
// socket and all state. It never uses an installed daemon or its defaults.
func fixture(t *testing.T) string {
	return fixtureWithAdmin(t, false)
}

// Administration is enabled only for tests that exercise native ownership
// boundaries, using a fresh owner-only credential inside the disposable root.
func fixtureWithAdmin(t *testing.T, administration bool) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "agw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	checkout, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	export := exec.Command("git", "archive", "--format=tar", baseline)
	export.Dir = strings.TrimSpace(string(checkout))
	archive, err := export.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(archive)); got != archiveSHA256 {
		t.Fatalf("baseline export checksum: %s", got)
	}
	archivePath := filepath.Join(root, "source.tar")
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(cmd *exec.Cmd) {
		t.Helper()
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", cmd.Args, err, output)
		}
	}
	run(exec.Command("tar", "-xf", archivePath, "-C", source))
	binary := filepath.Join(root, "aircd")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/aircd")
	build.Dir = source
	run(build)
	socket := filepath.Join(root, "irc.sock")
	log, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	args := []string{"--unix", socket, "--history", "32", "--history-file", filepath.Join(root, "history.jsonl"), "--sync", "fsync"}
	if administration {
		adminFile := filepath.Join(root, "admin.token")
		if err := auth.CreateCredential(adminFile); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--admin-token-file", adminFile)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"HOME=" + root, "XDG_CONFIG_HOME=" + root, "AIRC_STATE_DIR=" + root}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture baseline=%s archive_sha256=%s pid=%d", baseline, archiveSHA256, cmd.Process.Pid)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("fixture shutdown: %v", err)
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
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return socket
		}
		time.Sleep(10 * time.Millisecond)
	}
	output, _ := os.ReadFile(log.Name())
	t.Fatalf("fixture failed to listen: %s", output)
	return ""
}
