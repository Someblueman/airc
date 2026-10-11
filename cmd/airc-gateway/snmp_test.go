package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
)

func TestSNMPDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c := config.Defaults()
	c.Version = 1
	c.Realm = "snmp-discovery"
	c.StateDir = filepath.Join(root, "state")
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "unused.sock"), Account: "author", AccountTokenFile: filepath.Join(root, "account")}}
	target := core.Target{Kind: "room", Name: "#fixture"}
	c.Principals = []config.Principal{{ID: "device", Binding: "native", BearerTokenFile: filepath.Join(root, "bearer"), Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
	c.Listeners = []config.Listener{{ID: "snmp", Protocol: "snmp_v3", Address: conn.LocalAddr().String(), Enabled: true, Operations: []core.Operation{core.Send}, SNMP: &config.SNMP{Routes: []config.SNMPRoute{{EngineID: "8000000102030405", User: "fixture", Authentication: snmpwire.HMACSHA196, AuthenticationKeyFile: filepath.Join(root, "localized"), ContextEngineID: "8000000102030405", BoundAuthor: config.BoundAuthor{Nick: "device"}, Target: target, Notifications: []config.SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert"}}}}}}}
	// References are checked, but discovery must not consume credentials or bind
	// the already occupied port. Malformed key content would fail real startup.
	for _, name := range []string{"account", "bearer", "localized"} {
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
	if !strings.Contains(out.String(), `"status":"enabled_snmp_v3_partial"`) || !strings.Contains(out.String(), `"operations":["send"]`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("discovery started runtime", err)
	}
	if err := os.Chmod(filepath.Join(root, "localized"), 0644); err != nil {
		t.Fatal(err)
	}
	if c.CheckFiles() == nil {
		t.Fatal("public key file reference accepted")
	}
}
