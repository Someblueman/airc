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

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/testfs"
)

// currentAircdFixture is aircd built from this tree, for tests of behaviour
// newer than the pinned baseline (WEBIRC source forwarding, guests refused on
// a live nickname, account IDs on login). Each daemon owns its own state.
var currentAircdFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

func currentAircdBinary(t *testing.T) string {
	t.Helper()
	currentAircdFixture.once.Do(func() {
		root, err := testfs.MkdirTemp("agw-aircd-")
		if err != nil {
			currentAircdFixture.err = err
			return
		}
		currentAircdFixture.root = root
		binary := filepath.Join(root, "aircd")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/aircd")
		build.Dir = "../.."
		if output, err := build.CombinedOutput(); err != nil {
			currentAircdFixture.err = fmt.Errorf("building current aircd: %w\n%s", err, output)
			return
		}
		currentAircdFixture.binary = binary
	})
	if currentAircdFixture.err != nil {
		t.Fatal(currentAircdFixture.err)
	}
	return currentAircdFixture.binary
}

// currentNative starts a current-tree aircd that trusts a fresh gateway
// credential for WEBIRC. It returns the daemon's socket and the credential
// file the gateway upstream names.
func currentNative(t *testing.T) (socket, gatewayToken string) {
	t.Helper()
	binary := currentAircdBinary(t)
	root, err := testfs.MkdirTemp("agw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	gatewayToken = filepath.Join(root, "gateway.token")
	if err := auth.CreateCredential(gatewayToken); err != nil {
		t.Fatal(err)
	}
	return startNative(t, binary, root, false, "none", "--gateway-token-file", gatewayToken), gatewayToken
}
