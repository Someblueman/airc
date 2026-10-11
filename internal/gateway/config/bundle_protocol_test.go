package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestBundleProtocolMappingConfiguration(t *testing.T) {
	makeConfig := func() Config {
		c := Defaults()
		c.Version, c.Realm, c.StateDir = 1, "bp-fixture", "/tmp/bp-state"
		target := core.Target{Kind: "room", Name: "#fixture"}
		c.Upstream = &Upstream{Network: "unix", Address: "/tmp/native.sock"}
		c.Listeners = []Listener{{ID: "bp", Protocol: "bpv7", Enabled: UnixAvailable(), Operations: []core.Operation{core.Send}, BundleProtocol: &BundleProtocol{Source: "dtn://producer/", Destination: "dtn://gateway/send", Target: target}}}
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
		"target":         func(c *Config) { c.Listeners[0].BundleProtocol.Target.Name = "bad target" },
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
