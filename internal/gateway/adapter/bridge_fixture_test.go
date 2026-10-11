//go:build darwin || linux

package adapter

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testfs"
)

// bridgeAdapterFixture is a principal-free gateway journal and bridge
// dispatcher routing #external to #fixture; aircd is never contacted.
type bridgeAdapterFixture struct {
	c      config.Config
	v      *auth.Verifier
	s      *store.Store
	d      *dispatch.Dispatcher
	target core.Target
	routes []config.BridgeRoute
}

func newBridgeAdapterFixture(t *testing.T, ops ...core.Operation) bridgeAdapterFixture {
	t.Helper()
	return newBridgeRoomFixture(t, "#external", ops...)
}

func newBridgeRoomFixture(t *testing.T, room string, ops ...core.Operation) bridgeAdapterFixture {
	t.Helper()
	root, err := testfs.MkdirTemp("bridge-adapter-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "bridge", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	if len(ops) == 0 {
		ops = []core.Operation{core.Send}
	}
	f := bridgeAdapterFixture{c: c, target: core.Target{Kind: "room", Name: "#fixture"}}
	f.routes = []config.BridgeRoute{{Room: room, Target: f.target, Operations: ops}}
	if f.v, err = auth.New(c); err != nil {
		t.Fatal(err)
	}
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	if f.s, err = store.Open(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.Close() })
	if f.d, err = dispatch.ForBridge(f.s, f.v, b, nil, "network", f.routes); err != nil {
		t.Fatal(err)
	}
	return f
}

// guestJob waits for a guest author's job by request ID.
func (f bridgeAdapterFixture) guestJob(t *testing.T, nick, requestID string, wait time.Duration) (store.Job, error) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		job, err := f.s.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: nick}}, requestID)
		if err == nil || time.Now().After(deadline) {
			return job, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
