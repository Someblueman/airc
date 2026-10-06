package config

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func mqttConfig() Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mqtt-fixture", "/tmp/mqtt-state"
	target := core.Target{Kind: "room", Name: "#fixture"}
	c.Bindings = []Binding{{ID: "native", Network: "unix", Address: "/tmp/native.sock", Account: "author", AccountTokenFile: "/tmp/account"}}
	c.Principals = []Principal{{ID: "producer", Binding: "native", BearerTokenFile: "/tmp/bearer", SigningKeys: []SigningKey{{ID: "key", PublicKeyFile: "/tmp/key"}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
	c.Listeners = []Listener{{ID: "mqtt", Protocol: "mqtt", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: []core.Operation{core.Send}, MQTT: &MQTT{Routes: []MQTTRoute{{Principal: "producer", Topic: "airc/send", Target: target}}}}}
	return c
}

func TestMQTTConfiguration(t *testing.T) {
	c := mqttConfig()
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing":          func(c *Config) { c.Listeners[0].MQTT = nil },
		"wrong_protocol":   func(c *Config) { c.Listeners[0].Protocol = "http" },
		"cleartext":        func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", "" },
		"origins":          func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"empty_operations": func(c *Config) { c.Listeners[0].Operations = nil },
		"read":             func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send, core.HistoryRead} },
		"empty_routes":     func(c *Config) { c.Listeners[0].MQTT.Routes = nil },
		"duplicate": func(c *Config) {
			c.Listeners[0].MQTT.Routes = append(c.Listeners[0].MQTT.Routes, c.Listeners[0].MQTT.Routes[0])
		},
		"wildcard":            func(c *Config) { c.Listeners[0].MQTT.Routes[0].Topic = "airc/+" },
		"unknown":             func(c *Config) { c.Listeners[0].MQTT.Routes[0].Principal = "unknown" },
		"revoked":             func(c *Config) { c.Principals[0].Revoked = true },
		"no_bearer":           func(c *Config) { c.Principals[0].BearerTokenFile = "" },
		"no_signing":          func(c *Config) { c.Principals[0].SigningKeys = nil },
		"permission":          func(c *Config) { c.Principals[0].Operations = []core.Operation{core.ReceiptGet} },
		"target":              func(c *Config) { c.Listeners[0].MQTT.Routes[0].Target.Name = "#other" },
		"target_kind":         func(c *Config) { c.Listeners[0].MQTT.Routes[0].Target.Kind = "agent" },
		"unnormalized_target": func(c *Config) { c.Listeners[0].MQTT.Routes[0].Target.Name = " fixture " },
	} {
		t.Run(name, func(t *testing.T) {
			c := mqttConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe MQTT grant")
			}
		})
	}
	for _, count := range []int{128, 129} {
		c := mqttConfig()
		grant := c.Listeners[0].MQTT.Routes[0]
		c.Listeners[0].MQTT.Routes = nil
		for i := range count {
			grant.Topic = fmt.Sprintf("airc/send/%d", i)
			c.Listeners[0].MQTT.Routes = append(c.Listeners[0].MQTT.Routes, grant)
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
	// TLS and exact grants permit the now-wired opt-in service profile.
	c = mqttConfig()
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled MQTT listener refused", err)
	}
}
