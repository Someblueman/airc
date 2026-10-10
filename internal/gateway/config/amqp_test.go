package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func amqpConfig() Config {
	c := mqttConfig()
	c.ResponseSigningKey = &ResponseSigningKey{ID: "response", PrivateKeyFile: "/tmp/response"}
	c.CursorKeyFile = "/tmp/cursor"
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	c.Principals[0].Operations = ops
	c.Listeners = []Listener{{ID: "amqp", Protocol: "amqp", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: ops, AMQP: &AMQP{Routes: []AMQPRoute{{Principal: "producer", RequestAddress: "airc/request", ResponseAddress: "airc/response", Target: c.Principals[0].Targets[0], Operations: ops}}}}}
	return c
}

func TestAMQPConfiguration(t *testing.T) {
	c := amqpConfig()
	// Plain requests are attributed to the connection principal: only its bearer
	// credential is needed, never an envelope or response signing key.
	c.Principals[0].SigningKeys, c.ResponseSigningKey = nil, nil
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled AMQP", err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing":          func(c *Config) { c.Listeners[0].AMQP = nil },
		"wrong_protocol":   func(c *Config) { c.Listeners[0].Protocol = "http" },
		"cleartext":        func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", "" },
		"origins":          func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"empty_operations": func(c *Config) { c.Listeners[0].Operations = nil },
		"watch":            func(c *Config) { c.Listeners[0].Operations = append(c.Listeners[0].Operations, core.Watch) },
		"empty_routes":     func(c *Config) { c.Listeners[0].AMQP.Routes = nil },
		"duplicate": func(c *Config) {
			c.Listeners[0].AMQP.Routes = append(c.Listeners[0].AMQP.Routes, c.Listeners[0].AMQP.Routes[0])
		},
		"cross_collision": func(c *Config) {
			c.Listeners[0].AMQP.Routes[0].ResponseAddress = c.Listeners[0].AMQP.Routes[0].RequestAddress
		},
		"wildcard":                  func(c *Config) { c.Listeners[0].AMQP.Routes[0].RequestAddress = "airc/*" },
		"response_missing":          func(c *Config) { c.Listeners[0].AMQP.Routes[0].ResponseAddress = "" },
		"unknown":                   func(c *Config) { c.Listeners[0].AMQP.Routes[0].Principal = "unknown" },
		"revoked":                   func(c *Config) { c.Principals[0].Revoked = true },
		"no_bearer":                 func(c *Config) { c.Principals[0].BearerTokenFile = "" },
		"principal_permission":      func(c *Config) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"listener_permission":       func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send} },
		"unused_permission":         func(c *Config) { c.Listeners[0].AMQP.Routes[0].Operations = []core.Operation{core.Send} },
		"empty_route_operations":    func(c *Config) { c.Listeners[0].AMQP.Routes[0].Operations = nil },
		"duplicate_route_operation": func(c *Config) { c.Listeners[0].AMQP.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"target":                    func(c *Config) { c.Listeners[0].AMQP.Routes[0].Target.Name = "#other" },
		"target_kind":               func(c *Config) { c.Listeners[0].AMQP.Routes[0].Target.Kind = "agent" },
		"unnormalized_target":       func(c *Config) { c.Listeners[0].AMQP.Routes[0].Target.Name = " fixture " },
		"no_cursor":                 func(c *Config) { c.CursorKeyFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := amqpConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe AMQP grant")
			}
		})
	}
	for _, count := range []int{128, 129} {
		c := amqpConfig()
		grant := c.Listeners[0].AMQP.Routes[0]
		c.Listeners[0].AMQP.Routes = nil
		for i := range count {
			grant.RequestAddress, grant.ResponseAddress = fmt.Sprintf("request/%d", i), fmt.Sprintf("response/%d", i)
			c.Listeners[0].AMQP.Routes = append(c.Listeners[0].AMQP.Routes, grant)
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
}

func TestAMQPAddressBounds(t *testing.T) {
	for _, address := range []string{"", "a b", "a\n", "a\x00", "a#", "a+", "a*", "é", strings.Repeat("a", 256)} {
		if amqpAddress(address) {
			t.Fatalf("accepted %q", address)
		}
	}
	if !amqpAddress(strings.Repeat("a", 255)) || !amqpAddress("Airc/request-1_test.:2") {
		t.Fatal("refused literal bounded address")
	}
}

func TestAMQPDisjointRoutes(t *testing.T) {
	c := amqpConfig()
	first := &c.Listeners[0].AMQP.Routes[0]
	first.Operations = []core.Operation{core.Send}
	second := *first
	second.RequestAddress, second.ResponseAddress = "read/request", "read/response"
	second.Operations = []core.Operation{core.ContextRead, core.HistoryRead, core.Search}
	c.Listeners[0].AMQP.Routes = append(c.Listeners[0].AMQP.Routes, second)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].AMQP.Routes[1].RequestAddress = "airc/response"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted cross-route response/request collision")
	}
	c = amqpConfig()
	c.Listeners[0].Operations = []core.Operation{core.Send}
	c.Listeners[0].AMQP.Routes[0].Operations = []core.Operation{core.Send}
	c.CursorKeyFile = ""
	if err := c.Validate(); err != nil {
		t.Fatal("send-only grant requires an unused cursor", err)
	}
}
