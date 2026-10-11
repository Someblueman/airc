package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOSCOREDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	c.Listeners = []config.Listener{{ID: "oscore", Protocol: "coap_oscore", Address: "127.0.0.1:0", Enabled: config.UnixAvailable(), Operations: []core.Operation{core.Send}, OSCORE: &config.OSCORE{Binary: filepath.Join(root, "worker"), ContextFile: filepath.Join(root, "context")}}}
	for _, name := range []string{"worker", "context"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("discovery-only fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"capabilities", "--file", path}, &out); err != nil {
		t.Fatal(err)
	}
	status := "disabled_unavailable"
	if config.UnixAvailable() {
		status = "enabled_coap_oscore_partial"
	}
	if !strings.Contains(out.String(), `"status":"`+status+`"`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("discovery started runtime", err)
	}
}
