package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestParentStreamListenerValidation(t *testing.T) {
	for _, protocol := range []string{"stdio", "socketpair", "inherited"} {
		t.Run(protocol, func(t *testing.T) {
			base := Defaults()
			base.Version, base.Realm, base.StateDir = 1, "fixture", "/tmp/stdio-gateway-state"
			base.Listeners = []Listener{{ID: protocol, Protocol: protocol, Enabled: Available("stdio"), Operations: []core.Operation{core.Send}}}
			if err := base.Validate(); err != nil {
				t.Fatal(err)
			}
			if base.Listeners[0].Address != "" {
				t.Fatalf("stdio listener received an address default: %q", base.Listeners[0].Address)
			}
			for name, mutate := range map[string]func(*Config){
				"address": func(c *Config) { c.Listeners[0].Address = "/tmp/gateway.pipe" },
				"tls":     func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/tmp/cert", "/tmp/key" },
				"origins": func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"http://127.0.0.1"} },
				"mixed": func(c *Config) {
					c.Listeners[0].Enabled = true
					c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Enabled: true, Address: "127.0.0.1:12345"})
				},
				"duplicate": func(c *Config) { c.Listeners = append(c.Listeners, c.Listeners[0]) },
			} {
				t.Run(name, func(t *testing.T) {
					c := base
					c.Listeners = append([]Listener(nil), base.Listeners...)
					mutate(&c)
					if err := c.Validate(); err == nil {
						t.Fatal("accepted invalid stdio listener")
					}
				})
			}
		})
	}
}
