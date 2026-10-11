package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// The database role allowed to NOTIFY is the route's access gate: the route
// needs no producer credential.
func postgresNotifyConfig() Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "postgres-fixture", "/tmp/postgres-state"
	target := core.Target{Kind: "room", Name: "#fixture"}
	c.Upstream = &Upstream{Network: "unix", Address: "/tmp/native.sock"}
	c.PostgresNotify = []PostgresNotify{{ID: "pg", Enabled: true, Address: "127.0.0.1:5432", TLSServerName: "localhost", ClientCertFile: "/tmp/cert", ClientKeyFile: "/tmp/key", Database: "airc", Username: "airc_listener", Channel: "airc_requests", Route: PostgresNotifyRoute{Target: target}}}
	return c
}

func TestPostgresNotifyConfiguration(t *testing.T) {
	c := postgresNotifyConfig()
	if err := c.Validate(); err != nil {
		t.Fatal("guest route", err)
	}
	c.PostgresNotify[0].Route.BoundAuthor = &BoundAuthor{Nick: "pg-bot", AccountTokenFile: "/tmp/pg-bot.token"}
	if err := c.Validate(); err != nil {
		t.Fatal("bound route", err)
	}
	if RouteGuests(c.PostgresNotify[0].Route.BoundAuthor, nil) {
		t.Fatal("a bound route admitted guests by default")
	}
	no := false
	for name, mutate := range map[string]func(*Config){
		"no_author":  func(c *Config) { c.PostgresNotify[0].Route.Guests = &no },
		"bound_nick": func(c *Config) { c.PostgresNotify[0].Route.BoundAuthor = &BoundAuthor{Nick: "9bad"} },
		"bound_file": func(c *Config) {
			c.PostgresNotify[0].Route.BoundAuthor = &BoundAuthor{Nick: "pg-bot", AccountTokenFile: "token"}
		},
		"canonical": func(c *Config) { c.PostgresNotify[0].Route.Target.Name = " fixture " },
		"relative":  func(c *Config) { c.PostgresNotify[0].ClientKeyFile = "key" },
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
