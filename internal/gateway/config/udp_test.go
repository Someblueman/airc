package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func udpConfig(principals int) Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "realm", "/tmp/gateway-state"
	c.Bindings = []Binding{{ID: "binding", Network: "unix", Address: "/tmp/irc.sock", Account: "owner", AccountTokenFile: "/private/account"}}
	c.ResponseSigningKey = &ResponseSigningKey{ID: "response", PrivateKeyFile: "/private/response.key"}
	c.Listeners = []Listener{{ID: "udp", Protocol: "udp", Enabled: true, Address: "127.0.0.1:9001", Operations: []core.Operation{core.Send}}}
	for i := 0; i < principals; i++ {
		id := string(rune('a' + i))
		c.Principals = append(c.Principals, Principal{ID: id, Binding: "binding", SigningKeys: []SigningKey{{ID: "sign-" + id, PublicKeyFile: "/public/" + id}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#room"}}})
		c.ReturnRoutes = append(c.ReturnRoutes, ReturnRoute{ID: "route-" + id, Principal: id, Protocol: "udp", Listener: "udp", Destination: "127.0.0.1:9002", RecipientKeyID: "recipient-" + id, RecipientPublicKeyFile: "/public/recipient-" + id})
	}
	return c
}

func TestUDPConfigValidatesPerPrincipalRoutesAndIPv6(t *testing.T) {
	c := udpConfig(2)
	c.Listeners[0].Address = "[::1]:9001"
	c.ReturnRoutes[0].Destination = "[::1]:9002"
	c.ReturnRoutes[1].Destination = "[::1]:9003"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUDPConfigAllowsWildcardEphemeralBindAndDisabledRoutes(t *testing.T) {
	c := udpConfig(1)
	c.Listeners[0].Address = "[::]:0"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUDPConfigRejectsMissingOrAmbiguousRoutes(t *testing.T) {
	empty := udpConfig(0)
	if err := empty.Validate(); err == nil {
		t.Fatal("accepted enabled UDP without a principal or return route")
	}
	for name, mutate := range map[string]func(*Config){
		"missing_signer": func(c *Config) { c.ResponseSigningKey = nil },
		"missing_route":  func(c *Config) { c.ReturnRoutes = c.ReturnRoutes[:1] },
		"duplicate_route": func(c *Config) {
			c.ReturnRoutes = append(c.ReturnRoutes, c.ReturnRoutes[0])
			c.ReturnRoutes[1].ID = "duplicate"
		},
		"wrong_listener": func(c *Config) { c.ReturnRoutes[0].Listener = "other" },
		"multicast":      func(c *Config) { c.ReturnRoutes[0].Destination = "239.0.0.1:9002" },
		"zero_port":      func(c *Config) { c.ReturnRoutes[0].Destination = "127.0.0.1:0" },
		"wildcard":       func(c *Config) { c.ReturnRoutes[0].Destination = "0.0.0.0:9002" },
		"broadcast":      func(c *Config) { c.ReturnRoutes[0].Destination = "255.255.255.255:9002" },
	} {
		t.Run(name, func(t *testing.T) {
			c := udpConfig(2)
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid UDP configuration")
			}
		})
	}
}

func TestUDPConfigRejectsTLSAndOrigins(t *testing.T) {
	c := udpConfig(1)
	c.Listeners[0].AllowedOrigins = []string{"https://example.com"}
	if err := c.Validate(); err == nil {
		t.Fatal("accepted UDP origins")
	}
	c = udpConfig(1)
	c.Listeners[0].TLSCertFile = "/private/cert.pem"
	c.Listeners[0].TLSKeyFile = "/private/key.pem"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted UDP TLS")
	}
}
