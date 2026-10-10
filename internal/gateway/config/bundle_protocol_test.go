package config

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"testing"
)

func TestBundleProtocolMappingConfiguration(t *testing.T) {
	makeConfig := func() Config {
		c := Defaults()
		c.Version, c.Realm, c.StateDir = 1, "bp-fixture", "/tmp/bp-state"
		target := core.Target{Kind: "room", Name: "#fixture"}
		c.Bindings = []Binding{{ID: "native", Network: "unix", Address: "/tmp/native.sock", Account: "author", AccountTokenFile: "/tmp/account"}}
		c.Principals = []Principal{{ID: "producer", Binding: "native", SigningKeys: []SigningKey{{ID: "key", PublicKeyFile: "/tmp/key"}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
		c.Listeners = []Listener{{ID: "bp", Protocol: "bpv7", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send}, BundleProtocol: &BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Principal: "producer", Target: target}}}
		return c
	}
	c := makeConfig()
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "" {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing":        func(c *Config) { c.Listeners[0].BundleProtocol = nil },
		"wrong_protocol": func(c *Config) { c.Listeners[0].Protocol = "http" },
		"source":         func(c *Config) { c.Listeners[0].BundleProtocol.Source = "dtn://producer/app" },
		"destination":    func(c *Config) { c.Listeners[0].BundleProtocol.Destination = "dtn://gateway/" },
		"principal":      func(c *Config) { c.Listeners[0].BundleProtocol.Principal = "unknown" },
		"revoked":        func(c *Config) { c.Principals[0].Revoked = true },
		"signing":        func(c *Config) { c.Principals[0].SigningKeys = nil; c.Principals[0].BearerTokenFile = "/tmp/bearer" },
		"target":         func(c *Config) { c.Listeners[0].BundleProtocol.Target.Name = "#other" },
		"permission":     func(c *Config) { c.Principals[0].Operations = []core.Operation{core.HistoryRead} },
		"read":           func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send, core.HistoryRead} },
		"address":        func(c *Config) { c.Listeners[0].Address = "127.0.0.1:0" },
		"mixed": func(c *Config) {
			c.Listeners[0].Enabled = true
			c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Enabled: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := makeConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe mapping")
			}
		})
	}
}
