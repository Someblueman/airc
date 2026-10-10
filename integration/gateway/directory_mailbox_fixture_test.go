//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

type directoryMailboxFixture struct {
	*fileMailboxFixture
	process *gatewayProcess
}

func newDirectoryMailboxFixture(t *testing.T) *directoryMailboxFixture {
	t.Helper()
	f := prepareDirectoryMailboxFixture(t)
	f.start(t)
	return f
}

func prepareDirectoryMailboxFixture(t *testing.T) *directoryMailboxFixture {
	t.Helper()
	f := newFileMailboxFixture(t)
	for _, name := range []string{"inbox", "processing", "results"} {
		if err := os.Mkdir(filepath.Join(f.directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.config.Listeners[0].Protocol = "directory_mailbox"
	f.config.Listeners[0].DirectoryMailbox = &config.DirectoryMailbox{Principal: f.config.Principals[0].ID}
	return &directoryMailboxFixture{fileMailboxFixture: f}
}

func (f *directoryMailboxFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	if address := gatewayAddress(t, f.process); address != f.directory {
		t.Fatal("wrong directory readiness", address)
	}
}

func (f *directoryMailboxFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal("directory shutdown", f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("directory shutdown did not join")
	}
}
