package gateway_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The current gateway executable is immutable within a test process. Each
// invocation still owns its configuration, credentials, journal and lifetime.
var currentGatewayFixture struct {
	once   sync.Once
	root   string
	binary string
	digest string
	err    error
}

func currentGatewayBinary(t *testing.T) string {
	t.Helper()
	currentGatewayFixture.once.Do(func() {
		currentGatewayFixture.root, currentGatewayFixture.binary, currentGatewayFixture.digest, currentGatewayFixture.err = buildCurrentGateway()
	})
	if currentGatewayFixture.err != nil {
		t.Fatal(currentGatewayFixture.err)
	}
	t.Logf("current gateway sha256=%s", currentGatewayFixture.digest)
	return currentGatewayFixture.binary
}

func buildCurrentGateway() (string, string, string, error) {
	root, err := os.MkdirTemp("/tmp", "agw-current-")
	if err != nil {
		return "", "", "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	binary := filepath.Join(root, "airc-gateway")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"build", fmt.Sprintf("-race=%t", gatewayTestRace), "-trimpath", "-o", binary, "./cmd/airc-gateway"}
	build := exec.CommandContext(ctx, "go", args...)
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		return "", "", "", fmt.Errorf("building current gateway: %w\n%s", err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", "", "", err
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return "", "", "", err
	}
	complete = true
	return root, binary, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
