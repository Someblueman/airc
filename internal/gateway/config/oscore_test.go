package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func oscoreConfig() Config {
	c := udpConfig()
	c.Listeners = []Listener{{ID: "oscore", Protocol: "coap_oscore", Address: "[::]:0", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send}, OSCORE: &OSCORE{Binary: "/private/oscore-gateway", ContextFile: "/private/oscore.json"}}}
	return c
}

func TestOSCOREConfiguration(t *testing.T) {
	c := oscoreConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing_options":  func(c *Config) { c.Listeners[0].OSCORE = nil },
		"relative_binary":  func(c *Config) { c.Listeners[0].OSCORE.Binary = "worker" },
		"relative_context": func(c *Config) { c.Listeners[0].OSCORE.ContextFile = "context" },
		"wrong_protocol":   func(c *Config) { c.Listeners[0].Protocol = "http" },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/private/cert", "/private/key"
		},
		"hostname":  func(c *Config) { c.Listeners[0].Address = "localhost:9000" },
		"multicast": func(c *Config) { c.Listeners[0].Address = "239.0.0.1:9000" },
	} {
		t.Run(name, func(t *testing.T) {
			c := oscoreConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid native listener allowed")
			}
		})
	}
}
