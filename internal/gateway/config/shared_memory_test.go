package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
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
	c.ReturnRoutes[0].Protocol = "shared_memory"
	c.ReturnRoutes[0].Listener = "shared"
	c.ReturnRoutes[0].Destination = "/response"
	c.RecipientKeys = []RecipientKey{{ID: "ingress-key", PrivateKeyFile: "/private/ingress.key"}}
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

func TestSharedMemoryConfigurationAndProtection(t *testing.T) {
	c := sharedMemoryConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled shared_memory config rejected: %v", err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); (err == nil) != SharedMemoryAvailable() {
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
		{"response equals ingress", func(c *Config) { c.ReturnRoutes[0].Destination = c.Listeners[0].Address }, false},
		{"invalid response", func(c *Config) { c.ReturnRoutes[0].Destination = "/x/y" }, false},
		{"missing recipient", func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" }, false},
		{"missing public key", func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "relative" }, false},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }, false},
		{"origins", func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.test"} }, false},
		{"anonymous", func(c *Config) { c.Listeners[0].AnonymousPrincipal = "principal" }, false},
		{"shared root overlaps state", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory = c.StateDir + "/queues" }, false},
		{"filesystem root", func(c *Config) { c.Listeners[0].SharedMemory.LockDirectory = "/" }, false},
		{"missing return route", func(c *Config) { c.ReturnRoutes = nil }, true},
		{"missing signer", func(c *Config) { c.ResponseSigningKey = nil }, true},
		{"missing ingress recipient key", func(c *Config) { c.RecipientKeys = nil }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enabledOnly && !SharedMemoryAvailable() {
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
}

func TestSharedMemoryRejectsCrossPrincipalResponseReuse(t *testing.T) {
	c := sharedMemoryConfig(t)
	second := c.Principals[0]
	second.ID = "other"
	second.Binding = c.Bindings[0].ID
	second.Operations = []core.Operation{core.Send}
	c.Principals = append(c.Principals, second)
	c.ReturnRoutes = append(c.ReturnRoutes, ReturnRoute{ID: "other-route", Principal: "other", Protocol: "shared_memory", Listener: "shared", Destination: "/response", RecipientKeyID: "recipient", RecipientPublicKeyFile: "/public/other"})
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("shared response queue reused across principals")
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

func TestSharedMemoryResponseNameHasOneRootAndPrincipalGlobally(t *testing.T) {
	c := sharedMemoryConfig(t)
	second := c.Listeners[0]
	second.ID, second.Address, second.Enabled = "second", "/other-ingress", false
	second.SharedMemory = &SharedMemory{LockDirectory: filepath.Join(t.TempDir(), "other-queues")}
	c.Listeners = append(c.Listeners, second)
	c.ReturnRoutes = append(c.ReturnRoutes, ReturnRoute{ID: "other-route", Principal: c.Principals[0].ID, Protocol: "shared_memory", Listener: "second", Destination: "/response", RecipientKeyID: "recipient", RecipientPublicKeyFile: "/public/recipient"})
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("same response name opened under different roots accepted")
	}
}

func TestSharedMemoryResponseCannotAliasAnyIngress(t *testing.T) {
	c := sharedMemoryConfig(t)
	second := c.Listeners[0]
	second.ID, second.Address, second.Enabled = "second", "/other-ingress", false
	second.SharedMemory = &SharedMemory{LockDirectory: filepath.Join(t.TempDir(), "other-queues")}
	c.Listeners = append(c.Listeners, second)
	c.ReturnRoutes[0].Destination = second.Address
	if err := c.validateSharedMemory(); err == nil {
		t.Fatal("response name aliases another root's ingress")
	}
}

func TestSharedMemoryLocalPathsIgnoreKernelQueueNames(t *testing.T) {
	c := sharedMemoryConfig(t)
	c.Listeners = append(c.Listeners, Listener{ID: "posix", Protocol: "posix_mqueue", Address: "/kernel-name"})
	c.ReturnRoutes = append(c.ReturnRoutes, ReturnRoute{ID: "posix-return", Protocol: "posix_mqueue", Destination: "/kernel-response"})
	if paths := c.sharedMemoryLocalPaths(); len(paths) != 0 {
		t.Fatalf("kernel queue names were treated as filesystem paths: %v", paths)
	}
	c.Listeners = append(c.Listeners, Listener{ID: "unix", Protocol: "unixgram", Address: "/tmp/ingress.sock"})
	c.ReturnRoutes = append(c.ReturnRoutes, ReturnRoute{ID: "unix-return", Protocol: "unixgram", Destination: "/tmp/response.sock"})
	paths := c.sharedMemoryLocalPaths()
	if len(paths) != 2 || paths[0] != "/tmp/ingress.sock" || paths[1] != "/tmp/response.sock" {
		t.Fatalf("filesystem paths not retained: %v", paths)
	}
}
