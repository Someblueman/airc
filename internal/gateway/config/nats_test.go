package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func natsConfig() Config {
	c := stompConfig()
	l := &c.Listeners[0]
	r := l.STOMP.Routes[0]
	l.ID, l.Protocol, l.STOMP = "nats", "nats", nil
	l.NATS = &NATS{Routes: []NATSRoute{{Principal: r.Principal, RequestSubject: "airc.request", ResponseSubject: "airc.response", Target: r.Target, Operations: r.Operations}}}
	return c
}

func TestNATSConfiguration(t *testing.T) {
	c := natsConfig()
	if err := c.Validate(); err != nil || c.Listeners[0].Address != "127.0.0.1:0" {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err == nil {
		t.Fatal("enabled unfinished NATS listener")
	}
	for name, mutate := range map[string]func(*Config){
		"missing":          func(c *Config) { c.Listeners[0].NATS = nil },
		"wrong_protocol":   func(c *Config) { c.Listeners[0].Protocol = "http" },
		"cleartext":        func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "", "" },
		"half_tls":         func(c *Config) { c.Listeners[0].TLSKeyFile = "" },
		"origins":          func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"empty_operations": func(c *Config) { c.Listeners[0].Operations = nil },
		"watch":            func(c *Config) { c.Listeners[0].Operations = append(c.Listeners[0].Operations, core.Watch) },
		"empty_routes":     func(c *Config) { c.Listeners[0].NATS.Routes = nil },
		"duplicate": func(c *Config) {
			c.Listeners[0].NATS.Routes = append(c.Listeners[0].NATS.Routes, c.Listeners[0].NATS.Routes[0])
		},
		"collision":                   func(c *Config) { c.Listeners[0].NATS.Routes[0].ResponseSubject = "airc.request" },
		"wildcard":                    func(c *Config) { c.Listeners[0].NATS.Routes[0].RequestSubject = "airc.*" },
		"empty_subject":               func(c *Config) { c.Listeners[0].NATS.Routes[0].ResponseSubject = "" },
		"empty_token":                 func(c *Config) { c.Listeners[0].NATS.Routes[0].RequestSubject = "airc..request" },
		"unicode_subject":             func(c *Config) { c.Listeners[0].NATS.Routes[0].RequestSubject = "airc.é" },
		"oversize_subject":            func(c *Config) { c.Listeners[0].NATS.Routes[0].RequestSubject = strings.Repeat("a", 256) },
		"unknown":                     func(c *Config) { c.Listeners[0].NATS.Routes[0].Principal = "unknown" },
		"revoked":                     func(c *Config) { c.Principals[0].Revoked = true },
		"no_bearer":                   func(c *Config) { c.Principals[0].BearerTokenFile = "" },
		"no_signing":                  func(c *Config) { c.Principals[0].SigningKeys = nil },
		"principal_permission":        func(c *Config) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"listener_permission":         func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Send} },
		"unused_permission":           func(c *Config) { c.Listeners[0].NATS.Routes[0].Operations = []core.Operation{core.Send} },
		"empty_route_operations":      func(c *Config) { c.Listeners[0].NATS.Routes[0].Operations = nil },
		"duplicate_operation":         func(c *Config) { c.Listeners[0].NATS.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"unsupported_route_operation": func(c *Config) { c.Listeners[0].NATS.Routes[0].Operations = []core.Operation{core.ReceiptGet} },
		"target":                      func(c *Config) { c.Listeners[0].NATS.Routes[0].Target.Name = "#other" },
		"target_kind":                 func(c *Config) { c.Listeners[0].NATS.Routes[0].Target.Kind = "agent" },
		"unnormalized_target":         func(c *Config) { c.Listeners[0].NATS.Routes[0].Target.Name = " fixture " },
		"no_cursor":                   func(c *Config) { c.CursorKeyFile = "" },
		"no_signer":                   func(c *Config) { c.ResponseSigningKey = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := natsConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe NATS grant")
			}
		})
	}
}

func TestNATSRouteBoundsAndDisjointOperations(t *testing.T) {
	for _, count := range []int{128, 129} {
		c := natsConfig()
		grant := c.Listeners[0].NATS.Routes[0]
		c.Listeners[0].NATS.Routes = nil
		for i := range count {
			grant.RequestSubject, grant.ResponseSubject = fmt.Sprintf("request.%d", i), fmt.Sprintf("response.%d", i)
			c.Listeners[0].NATS.Routes = append(c.Listeners[0].NATS.Routes, grant)
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
	c := natsConfig()
	first := &c.Listeners[0].NATS.Routes[0]
	first.Operations = []core.Operation{core.Send}
	second := *first
	second.RequestSubject, second.ResponseSubject = "read.request", "read.response"
	second.Operations = []core.Operation{core.ContextRead, core.HistoryRead, core.Search}
	c.Listeners[0].NATS.Routes = append(c.Listeners[0].NATS.Routes, second)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].NATS.Routes[1].RequestSubject = "airc.response"
	if err := c.Validate(); err == nil {
		t.Fatal("cross-route subject collision")
	}
	c = natsConfig()
	c.Listeners[0].Operations = []core.Operation{core.Send}
	c.Listeners[0].NATS.Routes[0].Operations = []core.Operation{core.Send}
	c.CursorKeyFile = ""
	if err := c.Validate(); err != nil {
		t.Fatal("unused cursor required", err)
	}
	c.Listeners[0].NATS.Routes[0].RequestSubject = strings.Repeat("a", 255)
	if err := c.Validate(); err != nil {
		t.Fatal("exact subject bound", err)
	}
}

func TestNATSJSONContract(t *testing.T) {
	wire, err := json.Marshal(natsConfig())
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(wire)
	if err != nil || c.Listeners[0].NATS == nil || c.Listeners[0].NATS.Routes[0].RequestSubject != "airc.request" {
		t.Fatalf("parse: %v", err)
	}
	for _, wrong := range []string{
		strings.Replace(string(wire), "request_subject", "request_destination", 1),
		strings.Replace(string(wire), `"request_subject":"airc.request"`, `"request_subject":"airc.request","request_subject":"airc.other"`, 1),
		strings.Replace(string(wire), `"response_subject":"airc.response"`, `"response_subject":null`, 1),
		strings.Replace(string(wire), "request_subject", "Request_subject", 1),
	} {
		if _, err := Parse([]byte(wrong)); err == nil {
			t.Fatal("accepted invalid JSON contract")
		}
	}
}
