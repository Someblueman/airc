package config

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestZeroMQProfileConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"ipv4":     func(*Config) {},
		"ipv6":     func(c *Config) { c.Listeners[0].Address = "[::1]:0" },
		"default":  func(c *Config) { c.Listeners[0].Address = "" },
		"remote":   func(c *Config) { c.Listeners[0].Address = "192.0.2.1:5555" },
		"wildcard": func(c *Config) { c.Listeners[0].Address = "0.0.0.0:5555" },
		"dns":      func(c *Config) { c.Listeners[0].Address = "localhost:5555" },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile = "/private/cert"
			c.Listeners[0].TLSKeyFile = "/private/key"
		},
		"watch":   func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Watch} },
		"receipt": func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.ReceiptGet} },
	} {
		t.Run(name, func(t *testing.T) {
			c := udpConfig()
			c.CursorKeyFile = "/private/cursor"
			c.Listeners[0] = Listener{ID: "zmq", Protocol: "zeromq", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}
			mutate(&c)
			// The profile needs no TLS, as on IRC, and takes every operation.
			valid := name != "dns" && name != "tls" && name != "origins"
			if err := c.Validate(); (err == nil) != valid {
				t.Fatalf("valid=%v: %v", valid, err)
			}
		})
	}
}
