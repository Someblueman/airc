package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUnixCapabilitiesDoNotStartRuntime(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
			path := filepath.Join(root, "gateway.sock")
			c.Listeners = []config.Listener{{ID: "local", Protocol: "unix", Enabled: enabled, Address: path, Operations: []core.Operation{core.Send}}}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "config.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err = run([]string{"capabilities", "--file", file}, &out)
			if enabled && !config.UnixAvailable() {
				if err == nil || out.Len() != 0 {
					t.Fatal("unsupported Unix listener was advertised as enabled", err, out.String())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					Listeners []struct {
						Protocol   string
						Status     string
						Operations []core.Operation
					}
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Listeners) != 1 {
					t.Fatalf("invalid capabilities: %v %s", err, out.String())
				}
				wantStatus := "disabled"
				if !config.UnixAvailable() {
					wantStatus = "disabled_unavailable"
				}
				if enabled {
					wantStatus = "enabled_unix_partial"
				}
				listener := result.Listeners[0]
				if listener.Protocol != "unix" || listener.Status != wantStatus || enabled && (len(listener.Operations) != 1 || listener.Operations[0] != core.Send) || !enabled && len(listener.Operations) != 0 {
					t.Fatalf("incorrect Unix capabilities: %s", out.String())
				}
			}
			for _, resource := range []string{c.StateDir, path} {
				if _, err := os.Lstat(resource); !os.IsNotExist(err) {
					t.Fatalf("capabilities mutated %s: %v", resource, err)
				}
			}
		})
	}
}
