package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUARTRequiresExplicitDeviceTLSAndSingleSession(t *testing.T) {
	base := Defaults()
	base.Version, base.Realm, base.StateDir = 1, "fixture", "/tmp/uart-state"
	base.Listeners = []Listener{{ID: "uart", Protocol: "uart", Address: "/dev/tty-fixture", Enabled: UnixAvailable(), TLSCertFile: "/tmp/uart.crt", TLSKeyFile: "/tmp/uart.key", Operations: []core.Operation{core.Send, core.ReceiptGet}}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no device":       func(c *Config) { c.Listeners[0].Address = "" },
		"relative device": func(c *Config) { c.Listeners[0].Address = "tty" },
		"unclean device":  func(c *Config) { c.Listeners[0].Address = "/dev/../dev/tty" },
		"plaintext":       func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", "" },
		"no operations":   func(c *Config) { c.Listeners[0].Operations = nil },
		"another listener": func(c *Config) {
			c.Listeners[0].Enabled = true
			c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Enabled: true, Address: "127.0.0.1:1"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Listeners = append([]Listener(nil), base.Listeners...)
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid UART configuration")
			}
		})
	}
}

func TestUARTRejectsIgnoredEnabledBrokers(t *testing.T) {
	l := Listener{Protocol: "uart", Address: "/dev/tty-fixture", Enabled: true, TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/key", Operations: []core.Operation{core.Send}}
	for _, c := range []Config{
		{RedisPubSub: []RedisPubSub{{Enabled: true}}},
		{RedisStreams: []RedisStreams{{Enabled: true}}},
		{Kafka: []Kafka{{Enabled: true}}},
		{IRCBridges: []IRCBridge{{Enabled: true}}},
	} {
		if err := c.validateUART(l); err == nil {
			t.Fatal("UART silently ignored another enabled ingress")
		}
	}
}
