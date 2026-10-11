package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type sharedMemoryCapability struct {
	ID         string           `json:"id"`
	Protocol   string           `json:"protocol"`
	Status     string           `json:"status"`
	Operations []core.Operation `json:"operations"`
}

func sharedMemoryCLIConfig(t *testing.T, enabled bool) (config.Config, string) {
	t.Helper()
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "shared-cli", filepath.Join(root, "state")
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "upstream.sock")}
	c.Listeners = []config.Listener{{ID: "shared", Protocol: "shared_memory", Address: "/ingress", ReplyTo: "/response", Enabled: enabled, SharedMemory: &config.SharedMemory{LockDirectory: filepath.Join(root, "queue-root")}, Operations: []core.Operation{core.Send}}}
	return c, root
}

func writeSharedMemoryCLIConfig(t *testing.T, c config.Config, root string) string {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSharedMemoryCapabilitiesArePartialAndDoNotDisclosePaths(t *testing.T) {
	if !config.Available("shared_memory") {
		t.Skip("enabled shared_memory capability requires a supported ARM64 target")
	}
	c, root := sharedMemoryCLIConfig(t, true)
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
	if report.Runtime != "partial_not_running" || len(report.Listeners) != 1 {
		t.Fatalf("unexpected capabilities report: %+v", report)
	}
	listener := report.Listeners[0]
	if listener.ID != "shared" || listener.Protocol != "shared_memory" || listener.Status != "enabled_shared_memory_partial" || !reflect.DeepEqual(listener.Operations, []core.Operation{core.Send}) {
		t.Fatalf("unexpected shared-memory capabilities: %+v", listener)
	}
	for _, secret := range []string{root, c.StateDir, c.Listeners[0].Address, c.Listeners[0].SharedMemory.LockDirectory, c.Upstream.Address, c.Listeners[0].ReplyTo} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("capabilities disclosed configured path/name %q: %s", secret, output.String())
		}
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatalf("capabilities created runtime state: %v", err)
	}
}

func TestDisabledSharedMemoryCapabilitiesReflectTargetAvailability(t *testing.T) {
	c, root := sharedMemoryCLIConfig(t, false)
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
	if config.Available("shared_memory") {
		want = "disabled"
	}
	if len(report.Listeners) != 1 || report.Listeners[0].Protocol != "shared_memory" || report.Listeners[0].Status != want || len(report.Listeners[0].Operations) != 0 {
		t.Fatalf("disabled target status=%+v want=%q", report.Listeners, want)
	}
	if strings.Contains(output.String(), c.Listeners[0].SharedMemory.LockDirectory) || strings.Contains(output.String(), c.Listeners[0].ReplyTo) {
		t.Fatalf("capabilities disclosed shared-memory paths/names: %s", output.String())
	}
}

func TestSharedMemoryCLIRefusesInvalidRouteAndRootOptions(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"reply aliases ingress", func(c *config.Config) { c.Listeners[0].ReplyTo = c.Listeners[0].Address }},
		{"relative lock root", func(c *config.Config) { c.Listeners[0].SharedMemory.LockDirectory = "queues" }},
		{"shared-memory options on other protocol", func(c *config.Config) { c.Listeners[0].Protocol = "http" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, root := sharedMemoryCLIConfig(t, false)
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
