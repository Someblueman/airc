package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func sharedMemoryConfig(t *testing.T) Config {
	t.Helper()
	c := unixgramConfig(t)
	root := filepath.Join(t.TempDir(), "queues")
	c.Listeners[0].ID = "shared"
	c.Listeners[0].Protocol = "shared_memory"
	c.Listeners[0].Enabled = false
	c.Listeners[0].Address = "/ingress"
	c.Listeners[0].SharedMemory = &SharedMemory{LockDirectory: root}
	c.Listeners[0].ReplyTo = "/response"
	return c
}

func TestSharedMemoryNameIsBoundedSingleASCIIName(t *testing.T) {
	for _, name := range []string{"/A", "/a0", "/a_b-9.x", "/" + strings.Repeat("a", 30)} {
		if !sharedMemoryName(name) {
			t.Errorf("valid queue name refused: %q", name)
		}
	}
	for _, name := range []string{"", "a", "/", "/../x", "/a/b", "/_a", "/a b", "/é", "/" + strings.Repeat("a", 31)} {
		if sharedMemoryName(name) {
			t.Errorf("invalid queue name accepted: %q", name)
		}
	}
}

func TestSharedMemoryConfiguration(t *testing.T) {
	c := sharedMemoryConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled shared_memory config rejected: %v", err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); (err == nil) != Available("shared_memory") {
		t.Fatalf("platform gating mismatch: %v", err)
	}
}

func TestSharedMemoryConfigurationRefusals(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*Config)
		enabledOnly bool
	}{
		{"missing options", func(c *Config) { c.Listeners[0].SharedMemory = nil }, false},
		{"relative root", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory = "queues" }, false},
		{"noncanonical root", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory += "/.." }, false},
		{"overlong root", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory = "/" + strings.Repeat("x", 4096) }, false},
		{"invalid ingress", func(c *Config) { c.Listeners[0].Address = "/../queue" }, false},
		{"overlong ingress", func(c *Config) { c.Listeners[0].Address = "/" + strings.Repeat("x", 31) }, false},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }, false},
		{"origins", func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.test"} }, false},
		{"shared root overlaps state", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory = c.StateDir + "/queues" }, false},
		{"filesystem root", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory = "/" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enabledOnly && !Available("shared_memory") {
				t.Skip("enabled shared_memory requires a supported platform")
			}
			c := sharedMemoryConfig(t)
			c.Listeners[0].Enabled = tc.enabledOnly
			tc.mutate(&c)
			if err := c.validateSharedMemory(); err == nil {
				t.Fatal("invalid shared_memory configuration accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*Config){
		"reply equals ingress": func(c *Config) { c.Listeners[0].ReplyTo = c.Listeners[0].Address },
		"invalid reply":        func(c *Config) { c.Listeners[0].ReplyTo = "/x/y" },
		"missing reply":        func(c *Config) { c.Listeners[0].Enabled = true; c.Listeners[0].ReplyTo = "" },
	} {
		c := sharedMemoryConfig(t)
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Fatal(name, "accepted")
		}
	}
}

func TestSharedMemoryRootsAndIngressNames(t *testing.T) {
	c := sharedMemoryConfig(t)
	second := c.Listeners[0]
	second.ID, second.Address, second.Enabled = "second", "/second", false
	c.Listeners = append(c.Listeners, second)
	if err := c.validateSharedMemory(); err != nil {
		t.Fatalf("distinct names in a shared root rejected: %v", err)
	}
	c.Listeners[1].Address = c.Listeners[0].Address
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("duplicate ingress name in one root accepted")
	}
	c.Listeners[1].Address = "/second"
	c.Listeners[1].SharedMemory = &SharedMemory{LockDirectory: filepath.Join(c.Listeners[0].SharedMemory.LockDirectory, "child")}
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("nested roots accepted")
	}
}

func TestSharedMemoryNamesAreGlobalAcrossRoots(t *testing.T) {
	c := sharedMemoryConfig(t)
	second := c.Listeners[0]
	second.ID, second.Enabled = "second", false
	second.SharedMemory = &SharedMemory{LockDirectory: filepath.Join(t.TempDir(), "other-queues")}
	c.Listeners = append(c.Listeners, second)
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("same ingress name in different roots accepted")
	}
}

func TestSharedMemoryReplyCannotAliasAnyIngress(t *testing.T) {
	c := sharedMemoryConfig(t)
	second := c.Listeners[0]
	second.ID, second.Address, second.Enabled = "second", "/other-ingress", false
	second.SharedMemory = &SharedMemory{LockDirectory: filepath.Join(t.TempDir(), "other-queues")}
	c.Listeners = append(c.Listeners, second)
	c.Listeners[0].ReplyTo = second.Address
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("reply name aliases another root's ingress")
	}
}

func TestSharedMemoryLocalPathsIgnoreKernelQueueNames(t *testing.T) {
	c := sharedMemoryConfig(t)
	c.Listeners = append(c.Listeners, Listener{ID: "posix", Protocol: "posix_mqueue", Address: "/kernel-name", ReplyTo: "/kernel-response"})
	if paths := c.sharedMemoryLocalPaths(); len(paths) != 0 {
		t.Fatalf("kernel queue names were treated as filesystem paths: %v", paths)
	}
	c.Listeners = append(c.Listeners, Listener{ID: "unix", Protocol: "unixgram", Address: "/tmp/ingress.sock"})
	paths := c.sharedMemoryLocalPaths()
	if len(paths) != 1 || paths[0] != "/tmp/ingress.sock" {
		t.Fatalf("filesystem paths not retained: %v", paths)
	}
}
