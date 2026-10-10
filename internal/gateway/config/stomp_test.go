package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSTOMPJSONContract(t *testing.T) {
	wire, err := json.Marshal(stompConfig())
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(wire)
	if err != nil || c.Listeners[0].STOMP == nil || c.Listeners[0].STOMP.Routes[0].RequestDestination != "airc/request" {
		t.Fatalf("STOMP configuration parse: %v", err)
	}
	wrong := strings.Replace(string(wire), "request_destination", "request_address", 1)
	if _, err := Parse([]byte(wrong)); err == nil {
		t.Fatal("accepted incorrect published destination field")
	}
}

func stompConfig() Config {
	c := mqttConfig()
	c.ResponseSigningKey = &ResponseSigningKey{ID: "response", PrivateKeyFile: "/tmp/response"}
	c.CursorKeyFile = "/tmp/cursor"
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	c.Principals[0].Operations = ops
	c.Listeners = []Listener{{ID: "stomp", Protocol: "stomp", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: ops, STOMP: &STOMP{Routes: []STOMPRoute{{Principal: "producer", RequestDestination: "airc/request", ResponseDestination: "airc/response", Target: c.Principals[0].Targets[0], Operations: ops}}}}}
	return c
}

func TestSTOMPConfiguration(t *testing.T) {
	c := stompConfig()
	// Plain requests are attributed to the connection principal: only its bearer
	// credential is needed, never an envelope or response signing key.
	c.Principals[0].SigningKeys, c.ResponseSigningKey = nil, nil
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled STOMP", err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing":          func(c *Config) { c.Listeners[0].STOMP = nil },
		"wrong_protocol":   func(c *Config) { c.Listeners[0].Protocol = "http" },
		"cleartext":        func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", "" },
		"origins":          func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"empty_operations": func(c *Config) { c.Listeners[0].Operations = nil },
		"watch":            func(c *Config) { c.Listeners[0].Operations = append(c.Listeners[0].Operations, core.Watch) },
		"empty_routes":     func(c *Config) { c.Listeners[0].STOMP.Routes = nil },
		"duplicate": func(c *Config) {
			c.Listeners[0].STOMP.Routes = append(c.Listeners[0].STOMP.Routes, c.Listeners[0].STOMP.Routes[0])
		},
		"cross_collision": func(c *Config) {
			c.Listeners[0].STOMP.Routes[0].ResponseDestination = c.Listeners[0].STOMP.Routes[0].RequestDestination
		},
		"wildcard":                  func(c *Config) { c.Listeners[0].STOMP.Routes[0].RequestDestination = "airc/*" },
		"response_missing":          func(c *Config) { c.Listeners[0].STOMP.Routes[0].ResponseDestination = "" },
		"unknown":                   func(c *Config) { c.Listeners[0].STOMP.Routes[0].Principal = "unknown" },
		"revoked":                   func(c *Config) { c.Principals[0].Revoked = true },
		"no_bearer":                 func(c *Config) { c.Principals[0].BearerTokenFile = "" },
		"principal_permission":      func(c *Config) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"listener_permission":       func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send} },
		"unused_permission":         func(c *Config) { c.Listeners[0].STOMP.Routes[0].Operations = []core.Operation{core.Send} },
		"empty_route_operations":    func(c *Config) { c.Listeners[0].STOMP.Routes[0].Operations = nil },
		"duplicate_route_operation": func(c *Config) { c.Listeners[0].STOMP.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"target":                    func(c *Config) { c.Listeners[0].STOMP.Routes[0].Target.Name = "#other" },
		"target_kind":               func(c *Config) { c.Listeners[0].STOMP.Routes[0].Target.Kind = "agent" },
		"unnormalized_target":       func(c *Config) { c.Listeners[0].STOMP.Routes[0].Target.Name = " fixture " },
		"no_cursor":                 func(c *Config) { c.CursorKeyFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := stompConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe STOMP grant")
			}
		})
	}
	for _, count := range []int{128, 129} {
		c := stompConfig()
		grant := c.Listeners[0].STOMP.Routes[0]
		c.Listeners[0].STOMP.Routes = nil
		for i := range count {
			grant.RequestDestination, grant.ResponseDestination = fmt.Sprintf("request/%d", i), fmt.Sprintf("response/%d", i)
			c.Listeners[0].STOMP.Routes = append(c.Listeners[0].STOMP.Routes, grant)
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
}

func TestSTOMPAddressBounds(t *testing.T) {
	for _, address := range []string{"", "a b", "a\n", "a\x00", "a#", "a+", "a*", "é", strings.Repeat("a", 256)} {
		if stompDestination(address) {
			t.Fatalf("accepted %q", address)
		}
	}
	if !stompDestination(strings.Repeat("a", 255)) || !stompDestination("Airc/request-1_test.:2") {
		t.Fatal("refused literal bounded address")
	}
}

func TestSTOMPDisjointRoutes(t *testing.T) {
	c := stompConfig()
	first := &c.Listeners[0].STOMP.Routes[0]
	first.Operations = []core.Operation{core.Send}
	second := *first
	second.RequestDestination, second.ResponseDestination = "read/request", "read/response"
	second.Operations = []core.Operation{core.ContextRead, core.HistoryRead, core.Search}
	c.Listeners[0].STOMP.Routes = append(c.Listeners[0].STOMP.Routes, second)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].STOMP.Routes[1].RequestDestination = "airc/response"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted cross-route response/request collision")
	}
	c = stompConfig()
	c.Listeners[0].Operations = []core.Operation{core.Send}
	c.Listeners[0].STOMP.Routes[0].Operations = []core.Operation{core.Send}
	c.CursorKeyFile = ""
	if err := c.Validate(); err != nil {
		t.Fatal("send-only grant requires an unused cursor", err)
	}
}
