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

func TestCoAPUDPDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "realm", filepath.Join(root, "state")
	c.Bindings = []config.Binding{{ID: "binding", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "owner", AccountTokenFile: filepath.Join(root, "account")}}
	c.Principals = []config.Principal{{ID: "principal", Binding: "binding", SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: filepath.Join(root, "signer.pem")}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#room"}}}}
	c.Listeners = []config.Listener{{ID: "coap", Protocol: "coap_udp", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send, core.ReceiptGet}}}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "response", PrivateKeyFile: filepath.Join(root, "response.pem")}
	c.ReturnRoutes = []config.ReturnRoute{{ID: "return", Protocol: "coap_udp", Listener: "coap", Principal: "principal", Destination: "127.0.0.1:12345", RecipientKeyID: "reader", RecipientPublicKeyFile: filepath.Join(root, "reader.pem")}}
	for _, name := range []string{"account", "signer.pem", "response.pem", "reader.pem"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("discovery-only fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
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
	if !strings.Contains(out.String(), `"status":"enabled_coap_udp_partial"`) || !strings.Contains(out.String(), `"operations":["send","receipt.get"]`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("discovery created runtime", err)
	}
}
