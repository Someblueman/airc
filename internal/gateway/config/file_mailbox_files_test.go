package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestFileMailboxPathsDoNotAliasStateOrCredentials(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.Listeners = []Listener{{ID: "files", Protocol: "file_mailbox", Address: root, Enabled: Available("unix"), Operations: []core.Operation{core.Send}}}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	c.StateDir = root
	if err := c.CheckFiles(); err == nil {
		t.Fatal("mailbox aliases journal directory")
	}
	c.StateDir = filepath.Join(root, "state")
	token := filepath.Join(root, "response.json")
	if err := os.WriteFile(token, []byte("private credential"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Upstream = &Upstream{Network: "unix", Address: "/tmp/unused.sock", GatewayTokenFile: token}
	if err := c.CheckFiles(); err == nil {
		t.Fatal("mailbox output aliases the upstream credential")
	}
}
