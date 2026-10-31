//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/service"
	"github.com/Someblueman/airc/internal/testtls"
)

// airc service install generates this gateway's configuration without
// linking the configuration package (internal/service keeps its own carrier
// table and writes the JSON itself, so the carriers' dependencies stay out
// of airc). These tests hold that table and output to the registry and the
// parser here.

// Every carrier install offers is a listener that takes plain author
// requests or tunnels aircd's own protocol, and install's TLS form and
// socket family for it are the registry's.
func TestInstallerCarrierTableFollowsTheRegistry(t *testing.T) {
	t.Parallel()
	names := service.SupportedCarriers()
	if len(names) == 0 {
		t.Fatal("install offers no carriers")
	}
	forms := map[config.TLSPolicy]string{config.TLSForbidden: "forbidden", config.TLSOptional: "optional", config.TLSRequired: "required"}
	for _, name := range names {
		tr, ok := config.Lookup(name)
		if !ok {
			t.Errorf("%s: not a listener protocol", name)
			continue
		}
		if !config.AuthorCarrier(name) && !config.NativeCarrier(name) {
			t.Errorf("%s: takes neither plain author requests nor native IRC", name)
		}
		if got, want := service.CarrierTLS(name), forms[tr.TLS]; got != want {
			t.Errorf("%s: install has TLS %s, the registry %s", name, got, want)
		}
		network := "tcp"
		if tr.Network == "udp" {
			network = "udp"
		}
		if got := service.CarrierNetwork(name); got != network {
			t.Errorf("%s: install listens on %s, the registry on %s", name, got, network)
		}
	}
}

// installed is a service with a gateway whose credentials exist in an
// owner-only state directory, as airc service install leaves them.
func installed(t *testing.T, args []string, carriers string) service.Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	list, err := service.ParseCarriers(carriers, "127.0.0.1", dir)
	if err != nil {
		t.Fatal(err)
	}
	c := service.Config{Name: "default", Binary: "/opt/airc/aircd", StateDir: dir, Args: args, Gateway: &service.Gateway{Binary: "/opt/airc/airc-gateway", Carriers: list}}
	for _, path := range []string{c.GatewayTokenPath(), c.GatewayCursorKeyPath()} {
		if err := admin.CreateToken(path); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// What install generates for each carrier alone, with aircd's certificate
// and access token where the carrier needs TLS, parses as this gateway
// reads it, and the settings install leaves out take their defaults.
func TestInstallerConfigurationParsesForEveryCarrier(t *testing.T) {
	t.Parallel()
	cert := testtls.New(t)
	access := filepath.Join(filepath.Dir(cert.KeyPath), "access.token")
	if err := admin.CreateToken(access); err != nil {
		t.Fatal(err)
	}
	var plain []string
	for _, name := range service.SupportedCarriers() {
		args := []string{"--listen", "127.0.0.1:6667"}
		if service.CarrierTLS(name) == "required" {
			args = append(args, "--tls-cert", cert.CertPath, "--tls-key", cert.KeyPath, "--access-token-file", access)
		} else {
			plain = append(plain, name)
		}
		c := installed(t, args, name)
		c.Gateway.UpstreamServerName = "localhost"
		data, err := c.GatewayConfig()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		loaded, err := config.Parse(data)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, data)
		}
		if len(loaded.Listeners) != 1 || loaded.Listeners[0].Protocol != name || !loaded.Listeners[0].GuestsAllowed() {
			t.Fatalf("%s: listeners %+v", name, loaded.Listeners)
		}
	}
	// Every plaintext carrier in one install: MQTT takes requests on its
	// default topic without install saying so.
	c := installed(t, []string{"--listen", "127.0.0.1:6667"}, strings.Join(plain, ","))
	data, err := c.GatewayConfig()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range loaded.Listeners {
		ids = append(ids, l.ID)
		if l.Protocol == "mqtt" || l.Protocol == "mqtt_websocket" {
			if l.MQTT == nil || len(l.MQTT.Routes) != 1 || l.MQTT.Routes[0].Topic != config.DefaultMQTTTopic {
				t.Errorf("%s routes %+v", l.ID, l.MQTT)
			}
		}
	}
	if !slices.Equal(ids, plain) {
		t.Fatalf("listeners %v, carriers %v", ids, plain)
	}
}

// The generated file loads from disk as the gateway's job reads it: one
// upstream on aircd's socket with the gateway credential, the listeners
// in the operator's order, and the carrier list aircd advertises.
func TestInstallerConfigurationLoadsFromDisk(t *testing.T) {
	t.Parallel()
	c := installed(t, []string{"--listen", "0.0.0.0:6667", "--unix", "/run/user/1/airc.sock"}, "framed,http=0.0.0.0:8080,websocket,unix")
	data, err := c.GatewayConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.GatewayConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(c.GatewayStateDir(), 0700); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(c.GatewayConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if u := loaded.Upstream; u == nil || u.Network != "unix" || u.Address != "/run/user/1/airc.sock" || u.GatewayTokenFile != c.GatewayTokenPath() || u.AccessTokenFile != "" {
		t.Fatalf("upstream %+v", loaded.Upstream)
	}
	if loaded.StateDir != c.GatewayStateDir() || loaded.CursorKeyFile != c.GatewayCursorKeyPath() || loaded.Realm != "default" {
		t.Fatal(string(data))
	}
	var ids []string
	for _, l := range loaded.Listeners {
		ids = append(ids, l.ID+"@"+l.Address)
		if !l.Enabled || len(l.Operations) != 0 || !l.GuestsAllowed() || l.TLSCertFile != "" || l.PasswordFile != "" {
			t.Fatalf("listener %+v", l)
		}
	}
	if strings.Join(ids, " ") != "framed@127.0.0.1:6683 http@0.0.0.0:8080 websocket@127.0.0.1:6681 unix@"+c.StateDir+"/gateway.sock" {
		t.Fatal(ids)
	}
	if strings.Join(loaded.Carriers, ",") != "irc:/run/user/1/airc.sock,framed:6683,http:8080,websocket:6681,unix:"+c.StateDir+"/gateway.sock" {
		t.Fatal(loaded.Carriers)
	}
}
