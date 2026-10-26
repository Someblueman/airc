package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestPlainCoAPDiscoveryDoesNotStartRuntime(t *testing.T) {
	for _, protocol := range []string{"coap_udp"} {
		t.Run(protocol, func(t *testing.T) {
			root := t.TempDir()
			c := config.Defaults()
			c.Version, c.Realm, c.StateDir = 1, "realm", filepath.Join(root, "state")
			c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
			c.Listeners = []config.Listener{{ID: "coap", Protocol: protocol, Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send, core.ReceiptGet}}}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), `"status":"enabled_`+protocol+`_partial"`) || !strings.Contains(out.String(), `"operations":["send","receipt.get"]`) || strings.Contains(out.String(), root) {
				t.Fatal(out.String())
			}
			if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
				t.Fatal("discovery created runtime", err)
			}
		})
	}
}
