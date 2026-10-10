package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAppendLogExplicitOwnerConfiguration(t *testing.T) {
	fixture := func() Config {
		c := udpConfig(1)
		c.ResponseSigningKey, c.ReturnRoutes = nil, nil
		c.Listeners = []Listener{{ID: "queue", Protocol: "append_log", Address: "/tmp/queue", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send}, AppendLog: &AppendLog{Principal: "a"}}}
		return c
	}
	c := fixture()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing_owner":  func(c *Config) { c.Listeners[0].AppendLog = nil },
		"unknown_owner":  func(c *Config) { c.Listeners[0].AppendLog.Principal = "other" },
		"revoked":        func(c *Config) { c.Principals[0].Revoked = true },
		"unsigned":       func(c *Config) { c.Principals[0].SigningKeys = nil },
		"relative":       func(c *Config) { c.Listeners[0].Address = "queue" },
		"empty_address":  func(c *Config) { c.Listeners[0].Address = "" },
		"implicit_ops":   func(c *Config) { c.Listeners[0].Operations = nil },
		"watch":          func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Watch} },
		"wrong_protocol": func(c *Config) { c.Listeners[0].Protocol = "http"; c.Listeners[0].Address = "127.0.0.1:0" },
		"tls":            func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/tmp/cert", "/tmp/key" },
	} {
		t.Run(name, func(t *testing.T) {
			c := fixture()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid append-log owner configuration accepted")
			}
		})
	}
}

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
