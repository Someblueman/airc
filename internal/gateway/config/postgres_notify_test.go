package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// The route principal has no producer credential and the gateway has no
// recipient keys: the client-certificate LISTEN connection is the authority.
func postgresNotifyConfig() Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "postgres-fixture", "/tmp/postgres-state"
	target := core.Target{Kind: "room", Name: "#fixture"}
	c.Bindings = []Binding{{ID: "native", Network: "unix", Address: "/tmp/native.sock", Account: "author", AccountTokenFile: "/tmp/account"}}
	c.Principals = []Principal{{ID: "producer", Binding: "native", Operations: []core.Operation{core.Send}, Targets: []core.Target{target}}}
	c.PostgresNotify = []PostgresNotify{{ID: "pg", Enabled: true, Address: "127.0.0.1:5432", TLSServerName: "localhost", ClientCertFile: "/tmp/cert", ClientKeyFile: "/tmp/key", Database: "airc", Username: "airc_listener", Channel: "airc_requests", Route: PostgresNotifyRoute{Principal: "producer", Target: target}}}
	return c
}

func TestPostgresNotifyConfiguration(t *testing.T) {
	c := postgresNotifyConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"unknown":    func(c *Config) { c.PostgresNotify[0].Route.Principal = "unknown" },
		"revoked":    func(c *Config) { c.Principals[0].Revoked = true },
		"permission": func(c *Config) { c.Principals[0].Operations = []core.Operation{core.ReceiptGet} },
		"target":     func(c *Config) { c.PostgresNotify[0].Route.Target.Name = "#other" },
		"canonical":  func(c *Config) { c.PostgresNotify[0].Route.Target.Name = " fixture " },
		"relative":   func(c *Config) { c.PostgresNotify[0].ClientKeyFile = "key" },
		// Only a route principal may omit producer credentials.
		"unrouted": func(c *Config) {
			c.Principals = append(c.Principals, Principal{ID: "other", Binding: "native", Operations: []core.Operation{core.Send}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := postgresNotifyConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe PostgreSQL notification route")
			}
		})
	}
}
