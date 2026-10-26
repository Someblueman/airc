package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Only the immutable executable is shared. Callers own their process, server,
// credentials and state, and the benchmark's explicit binary override remains.
var nativeCLI struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func nativeCLIBinary(t testing.TB) string {
	t.Helper()
	nativeCLI.once.Do(func() {
		nativeCLI.root, nativeCLI.binary, nativeCLI.err = buildNativeCLI()
	})
	if nativeCLI.err != nil {
		t.Fatal(nativeCLI.err)
	}
	return nativeCLI.binary
}

func buildNativeCLI() (string, string, error) {
	root, err := os.MkdirTemp("", "airc-test-cli-")
	if err != nil {
		return "", "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	binary := filepath.Join(root, "airc")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Keep the subprocess build mode used by these checks, including GOFLAGS.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build native CLI: %w\n%s", err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", "", err
	}
	complete = true
	return root, binary, nil
}

func TestMain(m *testing.M) {
	code := m.Run()
	if nativeCLI.root != "" {
		_ = os.RemoveAll(nativeCLI.root)
	}
	if gatewayFixture.root != "" {
		_ = os.RemoveAll(gatewayFixture.root)
	}
	os.Exit(code)
}
