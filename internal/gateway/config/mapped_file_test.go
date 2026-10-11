package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func mappedFileConfig(t *testing.T) Config {
	t.Helper()
	c := unixgramConfig(t)
	root := filepath.Join(t.TempDir(), "queues")
	c.Listeners[0].ID = "mapped"
	c.Listeners[0].Protocol = "mapped_file"
	c.Listeners[0].Enabled = false
	c.Listeners[0].Address = filepath.Join(root, "ingress")
	c.Listeners[0].ReplyTo = filepath.Join(root, "response")
	return c
}

func TestMappedFileConfigurationAndPlatformGate(t *testing.T) {
	c := mappedFileConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled mapped_file config rejected: %v", err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); (err == nil) != Available("mapped_file") {
		t.Fatalf("platform gate mismatch: %v", err)
	}
}

func TestMappedFileConfigurationRefusals(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*Config)
		enabledOnly bool
	}{
		{"relative ingress", func(c *Config) { c.Listeners[0].Address = "queues/in" }, false},
		{"root ingress", func(c *Config) { c.Listeners[0].Address = "/" }, false},
		{"noncanonical ingress", func(c *Config) { c.Listeners[0].Address += "/.." }, false},
		{"overlong reply", func(c *Config) { c.Listeners[0].ReplyTo = "/" + strings.Repeat("x", 4096) }, false},
		{"reply equals ingress", func(c *Config) { c.Listeners[0].ReplyTo = c.Listeners[0].Address }, false},
		{"nested reply", func(c *Config) { c.Listeners[0].ReplyTo = filepath.Join(c.Listeners[0].Address, "child") }, false},
		{"reply in state", func(c *Config) { c.Listeners[0].ReplyTo = filepath.Join(c.StateDir, "reply") }, false},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }, false},
		{"origins", func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.test"} }, false},
		{"missing reply", func(c *Config) { c.Listeners[0].ReplyTo = "" }, true},
		{"missing mapped address", func(c *Config) { c.Listeners[0].Address = "" }, false},
		{"mapped mode option irrelevant shared memory config", func(c *Config) { c.Listeners[0].SharedMemory = &SharedMemory{LockDirectory: "/tmp/lock"} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enabledOnly && !Available("mapped_file") {
				t.Skip("enabled mapped_file requires a supported platform")
			}
			c := mappedFileConfig(t)
			c.Listeners[0].Enabled = tc.enabledOnly
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid mapped_file configuration accepted")
			}
		})
	}
}

func TestMappedFileRejectsFilesystemAliasesAndOverlaps(t *testing.T) {
	t.Run("state overlap", func(t *testing.T) {
		c := mappedFileConfig(t)
		c.StateDir = filepath.Join(c.Listeners[0].Address, "state")
		if err := c.validateMappedFile(); err == nil {
			t.Fatal("queue inside state directory accepted")
		}
	})
	t.Run("shared memory lock overlap", func(t *testing.T) {
		c := mappedFileConfig(t)
		c.Listeners = append(c.Listeners, Listener{ID: "shm", Protocol: "shared_memory", Address: "/other", SharedMemory: &SharedMemory{LockDirectory: filepath.Join(c.Listeners[0].Address, "locks")}})
		if err := c.validateMappedFile(); err == nil {
			t.Fatal("queue overlapping shared_memory lock root accepted")
		}
	})
	t.Run("local resource overlap", func(t *testing.T) {
		c := mappedFileConfig(t)
		c.Upstream.Address = filepath.Join(c.Listeners[0].Address, "upstream.sock")
		if err := c.validateMappedFile(); err == nil {
			t.Fatal("queue overlapping the upstream socket accepted")
		}
	})
}
