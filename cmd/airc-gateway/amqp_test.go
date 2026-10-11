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
	"github.com/Someblueman/airc/internal/testtls"
)

func TestAMQPDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "amqp-discovery", filepath.Join(root, "state")
	c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(root, "native.sock")}
	cert := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "amqp", Protocol: "amqp", Enabled: true, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send}, AMQP: &config.AMQP{Routes: []config.AMQPRoute{{RequestAddress: "request", ResponseAddress: "response", Operations: []core.Operation{core.Send}}}}}}
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
	if !strings.Contains(out.String(), `"status":"enabled_amqp_partial"`) || !strings.Contains(out.String(), `"operations":["send"]`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("discovery started runtime", err)
	}
}
