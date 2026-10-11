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

// The example executable is immutable; each call owns input and child lifetime.
type exampleClientFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

var framedClientFixture exampleClientFixture

func framedClientBinary(t *testing.T) string {
	return framedClientFixture.get(t, "framed-client")
}

func (fixture *exampleClientFixture) get(t *testing.T, name string) string {
	t.Helper()
	fixture.once.Do(func() {
		fixture.root, fixture.binary, fixture.err = buildExampleClient(name)
	})
	if fixture.err != nil {
		t.Fatal(fixture.err)
	}
	return fixture.binary
}

func buildExampleClient(name string) (string, string, error) {
	root, err := testfs.MkdirTemp("agw-" + name + "-")
	if err != nil {
		return "", "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(root)
		}
	}()
	binary := filepath.Join(root, name)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./examples/gateway/"+name)
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build %s example: %w\n%s", name, err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", "", err
	}
	complete = true
	return root, binary, nil
}
