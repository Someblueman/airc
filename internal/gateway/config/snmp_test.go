package config

import (
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/snmpwire"
	"testing"
)

func snmpConfig() Config {
	c := mqttConfig()
	c.Listeners = []Listener{{ID: "snmp", Protocol: "snmp_v3", Address: "0.0.0.0:0", Operations: []core.Operation{core.Send}, SNMP: &SNMP{Routes: []SNMPRoute{{EngineID: "8000000102030405", User: "fixture", Authentication: snmpwire.HMACSHA256192, AuthenticationKeyFile: "/tmp/snmp/key", ContextEngineID: "8000000102030405", Principal: "producer", Target: c.Principals[0].Targets[0], Notifications: []SNMPNotification{{OID: "1.3.6.1.4.1.123.1", Name: "Alert", Fields: []SNMPField{{OID: "1.3.6.1.4.1.123.2", Name: "Message", Type: "text", Required: true}}}}}}}}}
	return c
}
func TestSNMPConfiguration(t *testing.T) {
	c := snmpConfig()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(b)
	if err != nil || parsed.Listeners[0].SNMP.Routes[0].User != "fixture" {
		t.Fatal("grant parse", err)
	}
	c.Listeners[0].Enabled = true
	if c.Validate() == nil {
		t.Fatal("unfinished listener enabled")
	}
	cases := map[string]func(*Config){
		"missing":             func(c *Config) { c.Listeners[0].SNMP = nil },
		"wrong_protocol":      func(c *Config) { c.Listeners[0].Protocol = "http" },
		"operation":           func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.HistoryRead} },
		"TLS":                 func(c *Config) { c.Listeners[0].TLSCertFile = "/tmp/cert"; c.Listeners[0].TLSKeyFile = "/tmp/key" },
		"origin":              func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"revoked":             func(c *Config) { c.Principals[0].Revoked = true },
		"principal_operation": func(c *Config) { c.Principals[0].Operations = []core.Operation{core.ReceiptGet} },
		"empty":               func(c *Config) { c.Listeners[0].SNMP.Routes = nil },
		"too_many":            func(c *Config) { c.Listeners[0].SNMP.Routes = make([]SNMPRoute, 129) },
		"duplicate": func(c *Config) {
			r := c.Listeners[0].SNMP.Routes[0]
			c.Listeners[0].SNMP.Routes = append(c.Listeners[0].SNMP.Routes, r)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := snmpConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
	routes := map[string]func(*SNMPRoute){
		"engine_case":            func(r *SNMPRoute) { r.EngineID = "800000010203040A" },
		"engine_reserved":        func(r *SNMPRoute) { r.EngineID = "0000000000" },
		"context":                func(r *SNMPRoute) { r.ContextEngineID = "aa" },
		"user":                   func(r *SNMPRoute) { r.User = "" },
		"context_nul":            func(r *SNMPRoute) { r.ContextName = "a\x00" },
		"authentication":         func(r *SNMPRoute) { r.Authentication = "md5" },
		"relative_key":           func(r *SNMPRoute) { r.AuthenticationKeyFile = "key" },
		"unclean_key":            func(r *SNMPRoute) { r.AuthenticationKeyFile = "/tmp/../key" },
		"principal":              func(r *SNMPRoute) { r.Principal = "unknown" },
		"target":                 func(r *SNMPRoute) { r.Target.Name = "#other" },
		"notifications":          func(r *SNMPRoute) { r.Notifications = nil },
		"duplicate_notification": func(r *SNMPRoute) { r.Notifications = append(r.Notifications, r.Notifications[0]) },
		"oid":                    func(r *SNMPRoute) { r.Notifications[0].OID = "1.03.6" },
		"label":                  func(r *SNMPRoute) { r.Notifications[0].Name = "bad\nlabel" },
		"fields":                 func(r *SNMPRoute) { r.Notifications[0].Fields = make([]SNMPField, 17) },
		"field_type":             func(r *SNMPRoute) { r.Notifications[0].Fields[0].Type = "auto" },
		"reserved_field":         func(r *SNMPRoute) { r.Notifications[0].Fields[0].OID = "1.3.6.1.2.1.1.3.0" },
		"duplicate_field":        func(r *SNMPRoute) { n := &r.Notifications[0]; n.Fields = append(n.Fields, n.Fields[0]) },
	}
	for name, mutate := range routes {
		t.Run(name, func(t *testing.T) {
			c := snmpConfig()
			mutate(&c.Listeners[0].SNMP.Routes[0])
			if c.Validate() == nil {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
	c = snmpConfig()
	r := c.Listeners[0].SNMP.Routes[0]
	r.User = "other"
	r.Authentication = snmpwire.HMACSHA196
	r.Notifications[0].Fields = nil
	c.Listeners[0].SNMP.Routes = append(c.Listeners[0].SNMP.Routes, r)
	if err := c.Validate(); err != nil {
		t.Fatal("distinct user and explicit SHA1 grant", err)
	}
}
