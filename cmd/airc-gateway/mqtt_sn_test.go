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

func TestMQTTSNDiscoveryDoesNotStartRuntime(t *testing.T) {
	root := t.TempDir()
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mqtt-sn-discovery", filepath.Join(root, "state")
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: filepath.Join(root, "account")}}
	target := core.Target{Kind: "room", Name: "#fixture"}
	c.Principals = []config.Principal{{ID: "device", Binding: "native", BearerTokenFile: filepath.Join(root, "bearer"), SigningKeys: []config.SigningKey{{ID: "signer", PublicKeyFile: filepath.Join(root, "signer")}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
	c.RecipientKeys = []config.RecipientKey{{ID: "recipient", PrivateKeyFile: filepath.Join(root, "recipient")}}
	c.Listeners = []config.Listener{{ID: "sensor", Protocol: "mqtt_sn", Enabled: true, Operations: []core.Operation{core.Send}, MQTTSN: &config.MQTTSN{Routes: []config.MQTTSNRoute{{Principal: "device", TopicID: 7, Target: target}}}}}
	for _, name := range []string{"account", "bearer", "signer", "recipient"} {
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
	if !strings.Contains(out.String(), `"status":"enabled_mqtt_sn_partial"`) || !strings.Contains(out.String(), `"operations":["send"]`) || strings.Contains(out.String(), root) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("discovery started runtime", err)
	}
}
