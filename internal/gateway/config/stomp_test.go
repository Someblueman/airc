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
	c.CursorKeyFile = "/tmp/cursor"
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	c.Listeners = []Listener{{ID: "stomp", Protocol: "stomp", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: ops, STOMP: &STOMP{Routes: []STOMPRoute{{RequestDestination: "airc/request", ResponseDestination: "airc/response", Operations: ops}}}}}
	return c
}

func TestSTOMPConfiguration(t *testing.T) {
	c := stompConfig()
	// Plain requests name their authors and targets: TLS, the listener
	// password and explicit operations are optional.
	c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", ""
	c.Listeners[0].PasswordFile = "/tmp/password"
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled STOMP", err)
	}
	// Only an address: the listener policy's operations and the default
	// request and response destinations.
	bare := stompConfig()
	bare.Listeners[0] = Listener{ID: "stomp", Protocol: "stomp", Address: "127.0.0.1:1", Enabled: true}
	if err := bare.Validate(); err != nil || len(bare.Listeners[0].STOMP.Routes) != 1 || bare.Listeners[0].STOMP.Routes[0].RequestDestination != "airc.requests" || bare.Listeners[0].STOMP.Routes[0].ResponseDestination != "airc.responses" {
		t.Fatalf("address-only STOMP listener: %+v %v", bare.Listeners[0].STOMP, err)
	}
	for name, mutate := range map[string]func(*Config){
		"wrong_protocol": func(c *Config) { c.Listeners[0].Protocol = "http" },
		"origins":        func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"watch":          func(c *Config) { c.Listeners[0].Operations = append(c.Listeners[0].Operations, core.Watch) },
		"duplicate": func(c *Config) {
			c.Listeners[0].STOMP.Routes = append(c.Listeners[0].STOMP.Routes, c.Listeners[0].STOMP.Routes[0])
		},
		"cross_collision": func(c *Config) {
			c.Listeners[0].STOMP.Routes[0].ResponseDestination = c.Listeners[0].STOMP.Routes[0].RequestDestination
		},
		"wildcard":                  func(c *Config) { c.Listeners[0].STOMP.Routes[0].RequestDestination = "airc/*" },
		"response_missing":          func(c *Config) { c.Listeners[0].STOMP.Routes[0].ResponseDestination = "" },
		"listener_permission":       func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send} },
		"duplicate_route_operation": func(c *Config) { c.Listeners[0].STOMP.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"no_cursor":                 func(c *Config) { c.CursorKeyFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := stompConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe STOMP route")
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
