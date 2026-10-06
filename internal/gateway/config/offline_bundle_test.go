package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOfflineBundleConfiguration(t *testing.T) {
	base := Defaults()
	base.Version, base.Realm, base.StateDir = 1, "offline-fixture", "/tmp/offline-state"
	base.Listeners = []Listener{{ID: "offline", Protocol: "offline_bundle", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send}}}
	if err := base.Validate(); err != nil || base.Listeners[0].Address != "" {
		t.Fatal("valid offline configuration", err)
	}
	for name, mutate := range map[string]func(*Config){
		"address":  func(c *Config) { c.Listeners[0].Address = "/tmp/bundle" },
		"tls":      func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/tmp/cert", "/tmp/key" },
		"implicit": func(c *Config) { c.Listeners[0].Operations = nil },
		"read":     func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send, core.ReceiptGet} },
		"mixed": func(c *Config) {
			c.Listeners[0].Enabled = true
			c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Address: "127.0.0.1:0", Enabled: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Listeners = append([]Listener(nil), base.Listeners...)
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid offline configuration")
			}
		})
	}
}
