package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

const emptyConfig = `{"version":1,"realm":"fixture","state_dir":"/tmp/fixture-gateway-state","bindings":[],"principals":[],"listeners":[],"return_routes":[]}`

func TestStrictConfigAndDefaults(t *testing.T) {
	c, err := Parse([]byte(emptyConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Retry.Policy != core.Retry || c.Retry.SendAttempts != 100 || c.Retry.LookupAttempts != 100 || c.Limits.Sessions-c.Limits.Watches < 1 {
		t.Fatal("unsafe defaults")
	}
	for _, input := range []string{
		strings.Replace(emptyConfig, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(emptyConfig, `"version":1`, `"Version":1`, 1),
		strings.Replace(emptyConfig, `"version":1`, `"version":2`, 1),
		strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":null`, 1),
		strings.Replace(emptyConfig, `"bindings":[]`, `"bindings":[{"id":"x","NETWORK":"unix"}]`, 1),
		strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"unimplemented","enabled":true}]`, 1),
		strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http"},{"id":"x","protocol":"http"}]`, 1),
		strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http","address":"0.0.0.0:9000"}]`, 1),
		strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http","address":"127.0.0.1:notaport"}]`, 1),
		strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"x","protocol":"http","address":"127.0.0.1:65536"}]`, 1),
		strings.Replace(emptyConfig, `"realm":"fixture"`, `"realm":"fixture","limits":{"workers":0}`, 1),
		strings.Replace(emptyConfig, `"realm":"fixture"`, `"realm":"fixture","limits":{"sessions":8,"watches":8}`, 1),
		strings.Replace(emptyConfig, `"realm":"fixture"`, `"realm":"fixture","retry":{"send_attempts":101}`, 1),
		strings.Replace(emptyConfig, `"realm":"fixture"`, `"realm":"fixture","retry":{"policy":"forever"}`, 1),
		strings.Replace(emptyConfig, `"state_dir":"/tmp/fixture-gateway-state"`, `"state_dir":"relative"`, 1),
		emptyConfig + `{}`,
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("accepted invalid config: %s", input)
		}
	}
	c, err = Parse([]byte(strings.Replace(emptyConfig, `"listeners":[]`, `"listeners":[{"id":"example","protocol":"http"}]`, 1)))
	if err != nil || c.Listeners[0].Enabled || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatalf("listener default: %+v %v", c, err)
	}
}

func TestIdentityAndCredentialReferences(t *testing.T) {
	root := t.TempDir()
	secret := func(name string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.Bindings = []Binding{{ID: "upstream", Network: "unix", Address: filepath.Join(root, "irc.sock"), Account: "FixtureAuthor", AccountTokenFile: secret("account")}}
	c.Principals = []Principal{{ID: "device", Binding: "upstream", BearerTokenFile: secret("bearer"), Operations: []core.Operation{core.Send, core.HistoryRead}, Targets: []core.Target{{Kind: "direct", Name: "Alice"}, {Kind: "room", Name: "#Room"}}}}
	data, _ := json.Marshal(c)
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Principals[0].Targets[0].Name != "alice" || parsed.Principals[0].Targets[1].Name != "#Room" {
		t.Fatal("target normalization")
	}
	if err := parsed.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.StateDir); !os.IsNotExist(err) {
		t.Fatal("validation created state")
	}
	if err := os.Chmod(c.Principals[0].BearerTokenFile, 0644); err != nil {
		t.Fatal(err)
	}
	if err := parsed.CheckFiles(); err == nil {
		t.Fatal("accepted world-readable credential")
	}
	_ = os.Chmod(c.Principals[0].BearerTokenFile, 0600)
	parsed.Principals[0].BearerTokenFile = parsed.Bindings[0].AccountTokenFile
	if err := parsed.CheckFiles(); err == nil {
		t.Fatal("accepted aliased credential layers")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("accepted symlinked config")
	}
}
