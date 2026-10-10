package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func fifoConfig() Config {
	c := udpConfig(1)
	c.ResponseSigningKey, c.ReturnRoutes = nil, nil
	c.CursorKeyFile = "/private/cursor"
	c.Listeners = []Listener{{ID: "pair", Protocol: "fifo", Address: "/tmp/pair", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}, FIFO: &FIFO{Principal: "a"}}}
	return c
}

func TestFIFOExplicitSingleSenderConfiguration(t *testing.T) {
	c := fifoConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing-owner": func(c *Config) { c.Listeners[0].FIFO = nil },
		"unknown-owner": func(c *Config) { c.Listeners[0].FIFO.Principal = "other" },
		"revoked":       func(c *Config) { c.Principals[0].Revoked = true },
		"unsigned": func(c *Config) {
			c.Principals[0].SigningKeys = nil
			c.Principals[0].BearerTokenFile = "/private/bearer"
		},
		"multiple-senders": func(c *Config) { c.Principals = udpConfig(2).Principals },
		"relative":         func(c *Config) { c.Listeners[0].Address = "pair" },
		"empty-address":    func(c *Config) { c.Listeners[0].Address = "" },
		"implicit-ops":     func(c *Config) { c.Listeners[0].Operations = nil },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/private/cert", "/private/key"
		},
		"origins":        func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://localhost"} },
		"wrong-protocol": func(c *Config) { c.Listeners[0].Protocol = "http"; c.Listeners[0].Address = "127.0.0.1:0" },
		"mixed-listeners": func(c *Config) {
			c.Listeners[0].Enabled = true
			c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Address: "127.0.0.1:0", Enabled: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := fifoConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid FIFO accepted")
			}
		})
	}
	c = fifoConfig()
	c.Listeners[0].Enabled = false
	c.Principals = udpConfig(2).Principals
	c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Address: "127.0.0.1:0", Enabled: true})
	if err := c.Validate(); err != nil {
		t.Fatal("disabled FIFO restricted an independent active listener", err)
	}
	c = fifoConfig()
	c.Principals = udpConfig(2).Principals
	c.Principals[1].Revoked = true
	if err := c.Validate(); err != nil {
		t.Fatal("revoked principal prevented sole active sender", err)
	}
}

func TestFIFOReferencesCannotOverlap(t *testing.T) {
	root := t.TempDir()
	pair := filepath.Join(root, "pair")
	if err := os.Mkdir(pair, 0700); err != nil {
		t.Fatal(err)
	}
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.Listeners = []Listener{{ID: "pair", Protocol: "fifo", Address: pair}}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{pair, filepath.Join(pair, "future", "state"), root} {
		c.StateDir = state
		if err := c.CheckFiles(); err == nil {
			t.Fatal("FIFO/state overlap accepted", state)
		}
	}
	c.StateDir = filepath.Join(root, "state")
	credential := filepath.Join(pair, "credential.json")
	if err := os.WriteFile(credential, []byte("credential"), 0600); err != nil {
		t.Fatal(err)
	}
	c.CursorKeyFile = credential
	if err := c.CheckFiles(); err == nil {
		t.Fatal("credential within pair accepted")
	}
	c.CursorKeyFile = ""
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(pair, alias); err != nil {
		t.Fatal(err)
	}
	c.StateDir = filepath.Join(alias, "future", "state")
	if err := c.CheckFiles(); err == nil {
		t.Fatal("aliased state overlap accepted")
	}
	c.StateDir = filepath.Join(root, "state")
	c.Listeners = append(c.Listeners, Listener{Protocol: "unix", Address: filepath.Join(pair, "upstream.sock")})
	if err := c.CheckFiles(); err == nil {
		t.Fatal("socket within pair accepted")
	}
}
