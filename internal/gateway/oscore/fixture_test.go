//go:build darwin || linux

package oscore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type bridgeFixture struct {
	configuration config.Config
	store         *store.Store
	material      *auth.OSCOREContext
	key           store.OSCOREKey
}

func newBridgeFixture(t *testing.T) *bridgeFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	material, err := auth.CreateOSCOREContext(filepath.Join(root, "oscore.json"), auth.OSCOREOwner{Realm: c.Realm, Listener: "oscore"})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	key := store.OSCOREKey{Digest: digest, Listener: "oscore"}
	return &bridgeFixture{configuration: c, store: s, material: material, key: key}
}
