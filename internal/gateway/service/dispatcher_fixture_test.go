package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// testDispatcher is an author dispatcher over its own fixture journal and an
// upstream that is never dialled, for owners that only need one to exist.
func testDispatcher(t *testing.T) *dispatch.Dispatcher {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "dispatcher", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	reader, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dispatch.ForListener(journal, reader, nil, config.Listener{ID: "fixture", Protocol: "http", Operations: []core.Operation{core.Send}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
