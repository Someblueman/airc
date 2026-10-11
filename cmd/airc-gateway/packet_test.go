package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// Datagram listeners validate and report their operations without starting
// the runtime.
func TestPacketDiscoveryDoesNotStartRuntime(t *testing.T) {
	listeners := []config.Listener{
		{Protocol: "udp", Address: "127.0.0.1:9001"},
		{Protocol: "ipv4_multicast", Address: "239.255.1.2:9001", IPv4Multicast: &config.IPv4Multicast{Interface: "lo"}},
		{Protocol: "ipv6_multicast", Address: "[ff11::1234]:9001", IPv6Multicast: &config.IPv6Multicast{Interface: "lo"}},
	}
	if runtime.GOOS == "linux" {
		listeners = append(listeners, config.Listener{Protocol: "udp_lite", Address: "127.0.0.1:9001", UDPLite: &config.UDPLite{ChecksumCoverage: "full"}})
	}
	for _, l := range listeners {
		t.Run(l.Protocol, func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "packet-discovery", filepath.Join(root, "state")
			c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
			l.ID, l.Enabled, l.Operations = l.Protocol, true, []core.Operation{core.Send, core.ReceiptGet}
			c.Listeners = []config.Listener{l}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "gateway.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run([]string{"config", "validate", "--file", path}, &out); err != nil || strings.TrimSpace(out.String()) != "configuration valid; no runtime or listeners started" {
				t.Fatal(out.String(), err)
			}
			out.Reset()
			if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Runtime   string `json:"runtime"`
				Listeners []struct {
					ID         string           `json:"id"`
					Status     string           `json:"status"`
					Operations []core.Operation `json:"operations"`
				} `json:"listeners"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Runtime != "partial_not_running" || len(result.Listeners) != 1 || result.Listeners[0].ID != l.ID || result.Listeners[0].Status != "enabled_"+l.Protocol+"_partial" || !reflect.DeepEqual(result.Listeners[0].Operations, l.Operations) || strings.Contains(out.String(), root) {
				t.Fatal(out.String(), err)
			}
			if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
				t.Fatal("discovery started runtime", err)
			}
		})
	}
}
