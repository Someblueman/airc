//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"golang.org/x/sys/unix"
)

func TestFIFOAbsentInputWriterExpiresAndFreshChildRecovers(t *testing.T) {
	binary := currentGatewayBinary(t)
	socket, c, credentials, observer := stdioFixtureConfig(t)
	observer.Close()
	c = configureFIFOFixture(t, c)
	// Return reader exists, but no input writer is ever opened for this child.
	reader, err := os.OpenFile(filepath.Join(c.Listeners[0].Address, "output.fifo"), os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(c.Listeners[0].Address), "fifo-absent-input.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "serve-fifo", "--file", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil || ctx.Err() != nil {
		t.Fatal("input absence did not expire within bootstrap bound", err, ctx.Err())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "i/o timeout") {
		t.Fatal("absence diagnostics", stdout.String(), stderr.String())
	}
	noFIFOJobs(t, c)
	// This reader is still open, so constructor success proves the exited
	// child released directory ownership; it makes no authentication claim.
	stream, err := adapter.OpenFIFOStream(c.Listeners[0].Address)
	if err != nil {
		t.Fatal("absent-peer child retained FIFO ownership", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	healthy, join, _ := startFIFO(t, binary, c, syscall.SIGTERM, false)
	framedAuthenticatedSendAndBoundedResume(t, healthy, credentials, socket, true)
	join()
}
