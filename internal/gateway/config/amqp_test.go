package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func amqpConfig() Config {
	c := mqttConfig()
	c.CursorKeyFile = "/tmp/cursor"
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	c.Listeners = []Listener{{ID: "amqp", Protocol: "amqp", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: ops, AMQP: &AMQP{Routes: []AMQPRoute{{RequestAddress: "airc/request", ResponseAddress: "airc/response", Operations: ops}}}}}
	return c
}

func TestAMQPConfiguration(t *testing.T) {
	c := amqpConfig()
	// Plain requests name their authors and targets: TLS, the listener
	// password and explicit operations are optional.
	c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", ""
	c.Listeners[0].PasswordFile = "/tmp/password"
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled AMQP", err)
	}
	// Only an address: the listener policy's operations and the default
	// request and response addresses.
	bare := amqpConfig()
	bare.Listeners[0] = Listener{ID: "amqp", Protocol: "amqp", Address: "127.0.0.1:1", Enabled: true}
	if err := bare.Validate(); err != nil || len(bare.Listeners[0].AMQP.Routes) != 1 || bare.Listeners[0].AMQP.Routes[0].RequestAddress != "airc.requests" || bare.Listeners[0].AMQP.Routes[0].ResponseAddress != "airc.responses" {
		t.Fatalf("address-only AMQP listener: %+v %v", bare.Listeners[0].AMQP, err)
	}
	for name, mutate := range map[string]func(*Config){
		"wrong_protocol": func(c *Config) { c.Listeners[0].Protocol = "http" },
		"origins":        func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"watch":          func(c *Config) { c.Listeners[0].Operations = append(c.Listeners[0].Operations, core.Watch) },
		"duplicate": func(c *Config) {
			c.Listeners[0].AMQP.Routes = append(c.Listeners[0].AMQP.Routes, c.Listeners[0].AMQP.Routes[0])
		},
		"cross_collision": func(c *Config) {
			c.Listeners[0].AMQP.Routes[0].ResponseAddress = c.Listeners[0].AMQP.Routes[0].RequestAddress
		},
		"wildcard":                  func(c *Config) { c.Listeners[0].AMQP.Routes[0].RequestAddress = "airc/*" },
		"response_missing":          func(c *Config) { c.Listeners[0].AMQP.Routes[0].ResponseAddress = "" },
		"listener_permission":       func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send} },
		"duplicate_route_operation": func(c *Config) { c.Listeners[0].AMQP.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"no_cursor":                 func(c *Config) { c.CursorKeyFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := amqpConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe AMQP route")
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

func TestFixedRouteAddressBounds(t *testing.T) {
	for _, address := range []string{"", "a b", "a\n", "a\x00", "a#", "a+", "a*", "é", strings.Repeat("a", 256)} {
		if fixedRouteAddress(address) {
			t.Fatalf("accepted %q", address)
		}
	}
	if !fixedRouteAddress(strings.Repeat("a", 255)) || !fixedRouteAddress("Airc/request-1_test.:2") {
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
