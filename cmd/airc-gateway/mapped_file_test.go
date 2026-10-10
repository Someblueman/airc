package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func provisionMappedCLIDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "queue.dat"), make([]byte, 16<<10), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "transaction.lock"), make([]byte, 8), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"queue.dat", "transaction.lock"} {
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || info.Mode().Perm() != 0600 || !info.Mode().IsRegular() {
			t.Fatalf("bad fixture file %s: info=%v err=%v", filepath.Join(path, name), info, err)
		}
	}
}

func mappedCLIConfig(t *testing.T, enabled bool) (config.Config, string) {
	t.Helper()
	c, root := sharedMemoryCLIConfig(t, enabled)
	base := filepath.Join(root, "mapped")
	c.Listeners[0].Protocol = "mapped_file"
	c.Listeners[0].Address = filepath.Join(base, "ingress")
	c.Listeners[0].SharedMemory = nil
	c.ReturnRoutes[0].Protocol = "mapped_file"
	c.ReturnRoutes[0].Destination = filepath.Join(base, "response")
	provisionMappedCLIDirectory(t, c.Listeners[0].Address)
	provisionMappedCLIDirectory(t, c.ReturnRoutes[0].Destination)
	return c, root
}

func TestMappedFileCapabilitiesArePartialAndDoNotDisclosePaths(t *testing.T) {
	if !config.MappedFileAvailable() {
		t.Skip("enabled mapped_file capability requires a supported ARM64 target")
	}
	c, root := mappedCLIConfig(t, true)
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Runtime   string                   `json:"runtime"`
		Listeners []sharedMemoryCapability `json:"listeners"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal("decode capabilities", err, output.String())
	}
	if report.Runtime != "partial_not_running" || len(report.Listeners) != 1 || report.Listeners[0].Protocol != "mapped_file" || report.Listeners[0].Status != "enabled_mapped_file_partial" {
		t.Fatalf("unexpected mapped_file capability report: %+v", report)
	}
	for _, secret := range []string{root, c.StateDir, c.Listeners[0].Address, c.ReturnRoutes[0].Destination} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("capabilities disclosed configured path %q: %s", secret, output.String())
		}
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatalf("capabilities created runtime state: %v", err)
	}
}

func TestDisabledMappedFileCapabilitiesReflectTargetAvailability(t *testing.T) {
	c, root := mappedCLIConfig(t, false)
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Listeners []sharedMemoryCapability `json:"listeners"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal("decode capabilities", err, output.String())
	}
	want := "disabled_unavailable"
	if config.MappedFileAvailable() {
		want = "disabled"
	}
	if len(report.Listeners) != 1 || report.Listeners[0].Protocol != "mapped_file" || report.Listeners[0].Status != want || len(report.Listeners[0].Operations) != 0 {
		t.Fatalf("disabled target status=%+v want=%q", report.Listeners, want)
	}
	if strings.Contains(output.String(), c.Listeners[0].Address) || strings.Contains(output.String(), c.ReturnRoutes[0].Destination) {
		t.Fatalf("capabilities disclosed mapped_file paths: %s", output.String())
	}
}

func TestMappedFileCLIRefusesInvalidRoutesAndOptions(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"response aliases ingress", func(c *config.Config) { c.ReturnRoutes[0].Destination = c.Listeners[0].Address }},
		{"missing response recipient", func(c *config.Config) { c.ReturnRoutes[0].RecipientKeyID = "" }},
		{"TLS option", func(c *config.Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }},
		{"mapped protocol option on unrelated protocol", func(c *config.Config) { c.Listeners[0].Protocol = "http" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, root := mappedCLIConfig(t, false)
			tc.mutate(&c)
			path := writeSharedMemoryCLIConfig(t, c, root)
			for _, command := range [][]string{{"config", "validate", "--file", path}, {"capabilities", "--file", path}} {
				var output bytes.Buffer
				if err := run(command, &output); err == nil || output.Len() != 0 {
					t.Fatalf("invalid configuration accepted or reported capabilities: args=%v err=%v output=%s", command, err, output.String())
				}
			}
			if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
				t.Fatalf("configuration refusal created runtime state: %v", err)
			}
		})
	}
}

func TestMappedFileCLIRequiresPrivateFixedQueueResources(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, config.Config)
	}{
		{"wrong queue size", func(t *testing.T, c config.Config) {
			if err := os.WriteFile(filepath.Join(c.Listeners[0].Address, "queue.dat"), []byte("short"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"insecure lock file", func(t *testing.T, c config.Config) {
			if err := os.Chmod(filepath.Join(c.ReturnRoutes[0].Destination, "transaction.lock"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"queue aliases credential", func(t *testing.T, c config.Config) {
			credential := c.Bindings[0].AccountTokenFile
			if err := os.WriteFile(credential, make([]byte, 16<<10), 0600); err != nil {
				t.Fatal(err)
			}
			queue := filepath.Join(c.Listeners[0].Address, "queue.dat")
			if err := os.Remove(queue); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(credential, queue); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, root := mappedCLIConfig(t, false)
			tc.mutate(t, c)
			path := writeSharedMemoryCLIConfig(t, c, root)
			var output bytes.Buffer
			if err := run([]string{"config", "validate", "--file", path}, &output); err == nil || output.Len() != 0 {
				t.Fatalf("invalid queue resource accepted or path leaked: err=%v output=%s", err, output.String())
			}
		})
	}
}

func TestMappedFileCLICatchesNestedQueuesThroughSymlinkAncestor(t *testing.T) {
	c, root := mappedCLIConfig(t, false)
	alias := filepath.Join(root, "queue-alias")
	if err := os.Symlink(filepath.Dir(c.Listeners[0].Address), alias); err != nil {
		t.Fatal(err)
	}
	c.ReturnRoutes[0].Destination = filepath.Join(alias, filepath.Base(c.Listeners[0].Address), "nested")
	provisionMappedCLIDirectory(t, c.ReturnRoutes[0].Destination)
	path := writeSharedMemoryCLIConfig(t, c, root)
	var output bytes.Buffer
	if err := run([]string{"config", "validate", "--file", path}, &output); err == nil || output.Len() != 0 {
		t.Fatalf("nested symlink-aliased queue accepted or path leaked: err=%v output=%s", err, output.String())
	}
}
