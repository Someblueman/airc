package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendLogReferencesCannotOverlap(t *testing.T) {
	root := t.TempDir()
	queue := filepath.Join(root, "queue")
	for _, path := range []string{queue, filepath.Join(queue, "logs")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.Listeners = []Listener{{ID: "queue", Protocol: "append_log", Address: queue}}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{queue, filepath.Join(queue, "future", "state"), root} {
		c.StateDir = state
		if err := c.CheckFiles(); err == nil {
			t.Fatal("overlapping state accepted", state)
		}
	}
	c.StateDir = filepath.Join(root, "state")
	credential := filepath.Join(queue, "logs", "credential.json")
	if err := os.WriteFile(credential, []byte("credential"), 0600); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = credential
	if err := c.CheckFiles(); err == nil {
		t.Fatal("credential inside mailbox accepted")
	}
	c.CursorKeyFile = ""
	c.Listeners = append(c.Listeners, Listener{Protocol: "unix", Address: filepath.Join(queue, "future.sock")})
	if err := c.CheckFiles(); err == nil {
		t.Fatal("local socket inside mailbox accepted")
	}
	c.Listeners = c.Listeners[:1]
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(queue, alias); err != nil {
		t.Fatal(err)
	}
	c.StateDir = filepath.Join(alias, "future", "state")
	if err := c.CheckFiles(); err == nil {
		t.Fatal("aliased future state inside mailbox accepted")
	}
}
