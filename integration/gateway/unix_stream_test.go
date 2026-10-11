//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

func TestUnixStreamSharedOperations(t *testing.T) {
	shortGatewayTimer(t, readLeaseTimer)
	conn, credentials, stop, _, _ := unixStreamFixture(t)
	defer conn.Close()
	duplexOperations(t, credentials, false, "framed_stream", 3, func(r frameRequest) {
		writeFrame(t, conn, r)
	}, func() adapter.FrameResponse {
		return readFrame(t, conn)
	}, stop)
}

func TestUnixStreamAuthenticatedSendAndBoundedResume(t *testing.T) {
	conn, credentials, stop, path, socket := unixStreamFixture(t)
	defer conn.Close()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("Unix listener is not an owner-only socket", info, err)
	}
	directory, err := os.Stat(filepath.Dir(path))
	if err != nil || directory.Mode().Perm() != 0700 {
		t.Fatal("Unix listener directory is not owner-only", directory, err)
	}
	framedAuthenticatedSendAndBoundedResume(t, conn, credentials, socket, true)
	stop()
}
