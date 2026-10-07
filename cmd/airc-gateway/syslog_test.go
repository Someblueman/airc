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

func TestSyslogDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "syslog_tls-discovery", filepath.Join(root, "state")
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: filepath.Join(root, "account")}}
	target := core.Target{Kind: "room", Name: "#fixture"}
	c.Principals = []config.Principal{{ID: "device", Binding: "native", BearerTokenFile: filepath.Join(root, "bearer"), SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: filepath.Join(root, "signer")}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
	cert := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "syslog", Protocol: "syslog_tls", Enabled: true, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: []core.Operation{core.Send}, Syslog: &config.Syslog{Routes: []config.SyslogRoute{{Principal: "device", CertificateSHA256: strings.Repeat("a", 64), Target: target}}}}}
	for _, name := range []string{"account", "bearer", "signer"} {
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
	if !strings.Contains(out.String(), `"status":"enabled_syslog_tls_partial"`) || !strings.Contains(out.String(), `"operations":["send"]`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("discovery started runtime", err)
	}
}
