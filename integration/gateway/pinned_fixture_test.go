package gateway_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testfs"
)

// Only the checksum-verified immutable executable is shared. Every daemon still
// owns a separate state directory, accounts, socket and process lifetime.
var pinnedFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func TestMain(m *testing.M) {
	code := m.Run()
	for _, root := range []string{pinnedFixture.root, currentAircdFixture.root, currentGatewayFixture.root, framedClientFixture.root, udpClientFixture.root} {
		if root != "" {
			if err := os.RemoveAll(root); err != nil {
				fmt.Fprintln(os.Stderr, "cleaning fixture:", err)
				code = 1
			}
		}
	}
	os.Exit(code)
}

func pinnedFixtureBinary(t *testing.T) string {
	t.Helper()
	pinnedFixture.once.Do(func() {
		pinnedFixture.root, pinnedFixture.binary, pinnedFixture.err = buildPinnedFixture()
	})
	if pinnedFixture.err != nil {
		t.Fatal(pinnedFixture.err)
	}
	return pinnedFixture.binary
}

func buildPinnedFixture() (string, string, error) {
	if root, binary, _, err := prebuiltFixtureBinary("AGW_NATIVE_BINARY", "AGW_NATIVE_SHA256", "github.com/Someblueman/airc/cmd/aircd", false); err != nil || binary != "" {
		return root, binary, err
	}
	root, err := testfs.MkdirTemp("agw-pinned-")
	if err != nil {
		return "", "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	checkout, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", err
	}
	export := exec.Command("git", "archive", "--format=tar", baseline)
	export.Dir = strings.TrimSpace(string(checkout))
	archive, err := export.Output()
	if err != nil {
		return "", "", err
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(archive)); got != archiveSHA256 {
		return "", "", fmt.Errorf("baseline export checksum: %s", got)
	}
	archivePath := filepath.Join(root, "source.tar")
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		return "", "", err
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return "", "", err
	}
	if output, err := exec.Command("tar", "-xf", archivePath, "-C", source).CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("extracting pinned fixture: %w\n%s", err, output)
	}
	binary := filepath.Join(root, "aircd")
	buildCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-trimpath", "-o", binary, "./cmd/aircd")
	build.Dir = source
	if output, err := build.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("building pinned fixture: %w\n%s", err, output)
	}
	complete = true
	return root, binary, nil
}
