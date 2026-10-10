package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func mappedFileConfig(t *testing.T) Config {
	t.Helper()
	c := unixgramConfig(t)
	root := filepath.Join(t.TempDir(), "queues")
	c.Listeners[0].ID = "mapped"
	c.Listeners[0].Protocol = "mapped_file"
	c.Listeners[0].Enabled = false
	c.Listeners[0].Address = filepath.Join(root, "ingress")
	c.ReturnRoutes[0].Protocol = "mapped_file"
	c.ReturnRoutes[0].Listener = "mapped"
	c.ReturnRoutes[0].Destination = filepath.Join(root, "response")
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress.key"}}
	return c
}

func TestMappedFileConfigurationAndPlatformGate(t *testing.T) {
	c := mappedFileConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled mapped_file config rejected: %v", err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); (err == nil) != MappedFileAvailable() {
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
		{"overlong response", func(c *Config) { c.ReturnRoutes[0].Destination = "/" + strings.Repeat("x", 4096) }, false},
		{"response equals ingress", func(c *Config) { c.ReturnRoutes[0].Destination = c.Listeners[0].Address }, false},
		{"missing recipient key", func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" }, false},
		{"missing response public key", func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "relative" }, false},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }, false},
		{"origins", func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.test"} }, false},
		{"anonymous", func(c *Config) { c.Listeners[0].AnonymousPrincipal = "principal" }, false},
		{"missing route", func(c *Config) { c.ReturnRoutes = nil }, true},
		{"missing signing key", func(c *Config) { c.ResponseSigningKey = nil }, true},
		{"missing ingress recipient", func(c *Config) { c.RecipientKeys = nil }, true},
		{"missing mapped address", func(c *Config) { c.Listeners[0].Address = "" }, false},
		{"mapped mode option irrelevant shared memory config", func(c *Config) { c.Listeners[0].SharedMemory = &SharedMemory{LockDirectory: "/tmp/lock"} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enabledOnly && !MappedFileAvailable() {
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
	t.Run("nested queues", func(t *testing.T) {
		c := mappedFileConfig(t)
		c.ReturnRoutes[0].Destination = filepath.Join(c.Listeners[0].Address, "child")
		if err := c.validateMappedFile(); err == nil {
			t.Fatal("nested queue directories accepted")
		}
	})
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
	t.Run("path is not posix queue name", func(t *testing.T) {
		c := mappedFileConfig(t)
		c.ReturnRoutes = append(c.ReturnRoutes, ReturnRoute{ID: "posix", Principal: c.Principals[0].ID, Protocol: "posix_mqueue", Listener: "posix", Destination: "/same-name"})
		if err := c.validateMappedFile(); err != nil {
			t.Fatalf("POSIX queue name treated as filesystem path: %v", err)
		}
	})
	t.Run("local resource overlap", func(t *testing.T) {
		c := mappedFileConfig(t)
		c.Bindings[0].Network = "unix"
		c.Bindings[0].Address = filepath.Join(c.Listeners[0].Address, "upstream.sock")
		if err := c.validateMappedFile(); err == nil {
			t.Fatal("queue overlapping local binding accepted")
		}
	})
	t.Run("response route reuse", func(t *testing.T) {
		c := mappedFileConfig(t)
		other := c.ReturnRoutes[0]
		other.ID = "other"
		other.Principal = "another-principal"
		c.Principals = append(c.Principals, Principal{ID: other.Principal, Binding: c.Bindings[0].ID, SigningKeys: []SigningKey{{ID: "another", PublicKeyFile: "/public/another"}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#other"}}})
		other.Destination = c.ReturnRoutes[0].Destination
		c.ReturnRoutes = append(c.ReturnRoutes, other)
		if err := c.validateMappedFile(); err == nil {
			t.Fatal("response queue reuse accepted")
		}
	})
}
