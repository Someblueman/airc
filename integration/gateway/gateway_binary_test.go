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

	"github.com/Someblueman/airc/internal/testfs"
)

// The current gateway executable is immutable within a test process. Each
// invocation still owns its configuration, credentials, journal and lifetime.
var currentGatewayFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func currentGatewayBinary(t *testing.T) string {
	t.Helper()
	currentGatewayFixture.once.Do(func() {
		currentGatewayFixture.root, currentGatewayFixture.binary, currentGatewayFixture.err = buildCurrentGateway()
	})
	if currentGatewayFixture.err != nil {
		t.Fatal(currentGatewayFixture.err)
	}
	return currentGatewayFixture.binary
}

func buildCurrentGateway() (string, string, error) {
	root, err := testfs.MkdirTemp("agw-current-")
	if err != nil {
		return "", "", err
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
		return "", "", fmt.Errorf("building current gateway: %w\n%s", err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", "", err
	}
	complete = true
	return root, binary, nil
}
