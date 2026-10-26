package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestUnixCapabilitiesDoNotStartRuntime(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		enabled  bool
	}{{"unix", false}, {"unix", true}, {"unixgram", false}, {"unixpacket", false}, {"unixpacket", true}} {
		protocol, enabled := tc.protocol, tc.enabled
		t.Run(protocol+"_"+map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
			path := filepath.Join(root, "gateway.sock")
			if protocol == "unixgram" || protocol == "unixpacket" {
				base := ""
				if config.Available("unix") {
					base = "/tmp"
				}
				shortRoot, err := os.MkdirTemp(base, "agw-cap-")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(shortRoot) })
				path = filepath.Join(shortRoot, "gateway.sock")
			}
			c.Listeners = []config.Listener{{ID: "local", Protocol: protocol, Enabled: enabled, Address: path, Operations: []core.Operation{core.Send}}}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "config.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			available := config.Available("unix")
			if protocol == "unixpacket" {
				available = config.Available("unixpacket")
			}
			err = run([]string{"capabilities", "--file", file}, &out)
			if enabled && !available {
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
				if !available {
					wantStatus = "disabled_unavailable"
				}
				if enabled {
					wantStatus = "enabled_" + protocol + "_partial"
				}
				listener := result.Listeners[0]
				if listener.Protocol != protocol || listener.Status != wantStatus || enabled && (len(listener.Operations) != 1 || listener.Operations[0] != core.Send) || !enabled && len(listener.Operations) != 0 {
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
