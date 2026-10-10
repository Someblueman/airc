//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

type appendLogFixture struct {
	*fileMailboxFixture
	process    *gatewayProcess
	generation uint64
}

func newAppendLogFixture(t *testing.T) *appendLogFixture {
	t.Helper()
	f := prepareAppendLogFixture(t)
	f.start(t)
	return f
}

func prepareAppendLogFixture(t *testing.T) *appendLogFixture {
	t.Helper()
	f := newFileMailboxFixture(t)
	if err := os.Mkdir(filepath.Join(f.directory, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	f.config.Listeners[0].Protocol = "append_log"
	f.config.Listeners[0].AppendLog = &config.AppendLog{Principal: f.config.Principals[0].ID}
	return &appendLogFixture{fileMailboxFixture: f, generation: 1}
}

func (f *appendLogFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	if address := gatewayAddress(t, f.process); address != f.directory {
		t.Fatal("wrong append-log readiness", address)
	}
}

func (f *appendLogFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal("append-log shutdown", f.process.err, f.process.stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("append-log shutdown did not join")
	}
}
