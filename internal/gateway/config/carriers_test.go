package config

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestAdvertisedCarriersAreValidated(t *testing.T) {
	base := `{"version":1,"realm":"r","state_dir":"/tmp/gw","carriers":%s}`
	for value, ok := range map[string]bool{
		`["irc:6667","http:6680","unix:/run/airc/gateway.sock"]`: true,
		`["irc+tls:6697","websocket+tls:6681"]`:                  true,
		`[]`:                                                     true,
		`["http"]`:                                               false,
		`["http:6680","http:6680"]`:                              false,
		`["unix:relative"]`:                                      false,
	} {
		c, err := Parse([]byte(strings.Replace(base, "%s", value, 1)))
		if (err == nil) != ok {
			t.Errorf("%s: %v", value, err)
		}
		if ok && strings.Count(value, ":") != len(c.Carriers) {
			t.Errorf("%s: %v", value, c.Carriers)
		}
	}
}

func TestListenersMayShareOneRoleReference(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		path := dir + "/" + name
		if err := os.WriteFile(path, []byte("x\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cert, key, access, gateway := write("cert.pem"), write("key.pem"), write("access.token"), write("gateway.token")
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "r", dir+"/state"
	c.Upstream = &Upstream{Network: "tcp", Address: "127.0.0.1:6697", TLSServerName: "localhost", TLSCAFile: cert, AccessTokenFile: access, GatewayTokenFile: gateway}
	for _, id := range []string{"http", "websocket"} {
		c.Listeners = append(c.Listeners, Listener{ID: id, Protocol: id, Address: "127.0.0.1:0", TLSCertFile: cert, TLSKeyFile: key, PasswordFile: access})
	}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	// A path in two roles is still an alias.
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Listeners[1].PasswordFile = gateway },
		func(c *Config) { c.Listeners[1].PasswordFile = key },
		func(c *Config) { c.Listeners[1].TLSKeyFile = access },
		func(c *Config) { c.CursorKeyFile = access },
	} {
		bad := c
		bad.Listeners = slices.Clone(c.Listeners)
		mutate(&bad)
		if err := bad.CheckFiles(); err == nil {
			t.Errorf("accepted an alias across roles: %+v", bad.Listeners[1])
		}
	}
}
