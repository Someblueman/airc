package config

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// fixtureRoom is the room the configuration fixtures route to.
var fixtureRoom = core.Target{Kind: "room", Name: "#fixture"}

func mqttConfig() Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mqtt-fixture", "/tmp/mqtt-state"
	c.Upstream = &Upstream{Network: "unix", Address: "/tmp/native.sock"}
	c.Listeners = []Listener{{ID: "mqtt", Protocol: "mqtt", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: []core.Operation{core.Send}, MQTT: &MQTT{Routes: []MQTTRoute{{Topic: "airc/send"}}}}}
	return c
}

func TestMQTTConfiguration(t *testing.T) {
	c := mqttConfig()
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	// Payloads are plain requests naming their authors and targets: TLS, the
	// listener password and explicit operations are optional.
	c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", ""
	c.Listeners[0].PasswordFile = "/tmp/mqtt-password"
	c.Listeners[0].Address = "0.0.0.0:1883"
	c.Listeners[0].Operations = []core.Operation{core.Send, core.ProfileUpdate}
	c.Listeners[0].Targets = []core.Target{fixtureRoom}
	if err := c.Validate(); err != nil {
		t.Fatal("refused a plaintext author MQTT listener", err)
	}
	// Reads are allowed: MQTT 5 answers them on a response topic.
	c.Listeners[0].Operations = []core.Operation{core.Send, core.HistoryRead, core.Check}
	if err := c.Validate(); err != nil {
		t.Fatal("refused an MQTT listener allowing reads", err)
	}
	// Only an address: the listener policy's operations and one default topic.
	bare := mqttConfig()
	bare.Listeners[0] = Listener{ID: "mqtt", Protocol: "mqtt", Address: "127.0.0.1:1883", Enabled: true}
	if err := bare.Validate(); err != nil || len(bare.Listeners[0].MQTT.Routes) != 1 || bare.Listeners[0].MQTT.Routes[0].Topic != DefaultMQTTTopic {
		t.Fatalf("address-only MQTT listener: %+v %v", bare.Listeners[0].MQTT, err)
	}
	for name, mutate := range map[string]func(*Config){
		"wrong_protocol": func(c *Config) { c.Listeners[0].Protocol = "http" },
		"origins":        func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"duplicate": func(c *Config) {
			c.Listeners[0].MQTT.Routes = append(c.Listeners[0].MQTT.Routes, c.Listeners[0].MQTT.Routes[0])
		},
		"wildcard": func(c *Config) { c.Listeners[0].MQTT.Routes[0].Topic = "airc/+" },
	} {
		t.Run(name, func(t *testing.T) {
			c := mqttConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe MQTT route")
			}
		})
	}
	for _, count := range []int{128, 129} {
		c := mqttConfig()
		c.Listeners[0].MQTT.Routes = nil
		for i := range count {
			c.Listeners[0].MQTT.Routes = append(c.Listeners[0].MQTT.Routes, MQTTRoute{Topic: fmt.Sprintf("airc/send/%d", i)})
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
	c = mqttConfig()
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled MQTT listener refused", err)
	}
}
