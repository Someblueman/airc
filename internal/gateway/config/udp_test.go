package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// udpConfig is an author UDP listener behind the single upstream.
func udpConfig() Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "realm", "/tmp/gateway-state"
	c.Upstream = &Upstream{Network: "unix", Address: "/tmp/irc.sock"}
	c.Listeners = []Listener{{ID: "udp", Protocol: "udp", Enabled: true, Address: "127.0.0.1:9001", Operations: []core.Operation{core.Send}}}
	return c
}

func TestUDPConfigAcceptsIPv6WildcardAndDisabled(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9001", "[::1]:9001", "[::]:0"} {
		c := udpConfig()
		c.Listeners[0].Address = address
		if err := c.Validate(); err != nil {
			t.Fatal(address, err)
		}
		c.Listeners[0].Enabled = false
		if err := c.Validate(); err != nil {
			t.Fatal(address, err)
		}
	}
}

func TestUDPConfigRejectsTLSAndOrigins(t *testing.T) {
	c := udpConfig()
	c.Listeners[0].AllowedOrigins = []string{"https://example.com"}
	if err := c.Validate(); err == nil {
		t.Fatal("accepted UDP origins")
	}
	c = udpConfig()
	c.Listeners[0].TLSCertFile = "/private/cert.pem"
	c.Listeners[0].TLSKeyFile = "/private/key.pem"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted UDP TLS")
	}
}
