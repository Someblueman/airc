package gateway_test

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

// The example executable is immutable; each call owns input and child lifetime.
var framedClientFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func framedClientBinary(t *testing.T) string {
	t.Helper()
	framedClientFixture.once.Do(func() {
		framedClientFixture.root, framedClientFixture.binary, framedClientFixture.err = buildFramedClient()
	})
	if framedClientFixture.err != nil {
		t.Fatal(framedClientFixture.err)
	}
	return framedClientFixture.binary
}

func buildFramedClient() (string, string, error) {
	root, err := os.MkdirTemp("/tmp", "agw-framed-client-")
	if err != nil {
		return "", "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	binary := filepath.Join(root, "framed-client")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./examples/gateway/framed-client")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build framed client: %w\n%s", err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", "", err
	}
	complete = true
	return root, binary, nil
}
