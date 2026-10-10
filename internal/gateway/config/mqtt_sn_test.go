package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func mqttSNConfig() Config {
	c := mqttConfig()
	c.Principals[0].BearerTokenFile = ""
	target := c.Principals[0].Targets[0]
	c.Listeners = []Listener{{ID: "sensor", Protocol: "mqtt_sn", Operations: []core.Operation{core.Send}, MQTTSN: &MQTTSN{Routes: []MQTTSNRoute{{Principal: "producer", TopicID: 7, Target: target}}}}}
	return c
}

func TestMQTTSNConfiguration(t *testing.T) {
	c := mqttSNConfig()
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing":        func(c *Config) { c.Listeners[0].MQTTSN = nil },
		"wrong_protocol": func(c *Config) { c.Listeners[0].Protocol = "udp" },
		"tls":            func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/tmp/cert", "/tmp/key" },
		"origins":        func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"operations":     func(c *Config) { c.Listeners[0].Operations = nil },
		"read":           func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send, core.HistoryRead} },
		"empty_routes":   func(c *Config) { c.Listeners[0].MQTTSN.Routes = nil },
		"duplicate": func(c *Config) {
			c.Listeners[0].MQTTSN.Routes = append(c.Listeners[0].MQTTSN.Routes, c.Listeners[0].MQTTSN.Routes[0])
		},
		"zero":                      func(c *Config) { c.Listeners[0].MQTTSN.Routes[0].TopicID = 0 },
		"reserved":                  func(c *Config) { c.Listeners[0].MQTTSN.Routes[0].TopicID = 0xffff },
		"unknown":                   func(c *Config) { c.Listeners[0].MQTTSN.Routes[0].Principal = "unknown" },
		"revoked":                   func(c *Config) { c.Principals[0].Revoked = true },
		"unsigned":                  func(c *Config) { c.Principals[0].SigningKeys = nil; c.Principals[0].BearerTokenFile = "/tmp/bearer" },
		"permission":                func(c *Config) { c.Principals[0].Operations = []core.Operation{core.ReceiptGet} },
		"target":                    func(c *Config) { c.Listeners[0].MQTTSN.Routes[0].Target.Name = "#other" },
		"target_kind":               func(c *Config) { c.Listeners[0].MQTTSN.Routes[0].Target.Kind = "agent" },
		"address":                   func(c *Config) { c.Listeners[0].Address = "example.com:1884" },
		"enabled_without_recipient": func(c *Config) { c.Listeners[0].Enabled = true },
	} {
		t.Run(name, func(t *testing.T) {
			c := mqttSNConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("accepted unsafe or unwired sensor configuration")
			}
		})
	}
	for _, count := range []int{128, 129} {
		c := mqttSNConfig()
		grant := c.Listeners[0].MQTTSN.Routes[0]
		c.Listeners[0].MQTTSN.Routes = nil
		for i := range count {
			grant.TopicID = uint16(i + 1)
			c.Listeners[0].MQTTSN.Routes = append(c.Listeners[0].MQTTSN.Routes, grant)
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
	c = mqttSNConfig()
	c.RecipientKeys = []RecipientKey{{ID: "recipient", PrivateKeyFile: "/tmp/recipient"}}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("encrypted opt-in MQTT-SN refused", err)
	}
}
