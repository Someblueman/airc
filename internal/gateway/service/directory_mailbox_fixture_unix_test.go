//go:build darwin || linux

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// directoryServiceConfig is a lifecycle fixture with an author upstream
// (never dialled) and one directory mailbox listener.
func directoryServiceConfig(t *testing.T) config.Config {
	t.Helper()
	c := upstreamServiceConfig(t)
	queue := filepath.Join(filepath.Dir(c.StateDir), "queue")
	for _, path := range []string{queue, filepath.Join(queue, "inbox"), filepath.Join(queue, "processing"), filepath.Join(queue, "results")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.Listeners = []config.Listener{{ID: "queue", Protocol: "directory_mailbox", Address: queue, Enabled: true, Operations: []core.Operation{core.Send}}}
	return c
}

// upstreamServiceConfig is a listener-free lifecycle fixture whose author
// upstream is a Unix socket in the fixture's private root.
func upstreamServiceConfig(t *testing.T) config.Config {
	t.Helper()
	c := lifecycleConfig(t)
	root, err := filepath.EvalSymlinks(filepath.Dir(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	c.StateDir = filepath.Join(root, "state")
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	return c
}
