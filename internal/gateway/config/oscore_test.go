package config

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"testing"
)

func oscoreConfig() Config {
	c := udpConfig(1)
	c.ResponseSigningKey, c.ReturnRoutes = nil, nil
	c.Listeners = []Listener{{ID: "oscore", Protocol: "coap_oscore", Address: "[::]:0", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send}, OSCORE: &OSCORE{Binary: "/private/oscore-gateway", ContextFile: "/private/oscore.json", Principal: "a"}}}
	return c
}

func TestOSCOREConfigurationFixedOwner(t *testing.T) {
	c := oscoreConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing_options":  func(c *Config) { c.Listeners[0].OSCORE = nil },
		"relative_binary":  func(c *Config) { c.Listeners[0].OSCORE.Binary = "worker" },
		"relative_context": func(c *Config) { c.Listeners[0].OSCORE.ContextFile = "context" },
		"unknown_owner":    func(c *Config) { c.Listeners[0].OSCORE.Principal = "other" },
		"revoked_owner":    func(c *Config) { c.Principals[0].Revoked = true },
		"unsigned_owner":   func(c *Config) { c.Principals[0].SigningKeys = nil },
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
