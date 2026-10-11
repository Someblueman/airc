package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// redisConfig needs no principal or signing material: Redis ACLs decide who
// may publish on the route, and requests name their authors.
func redisConfig() Config {
	c := Defaults()
	c.Version, c.Realm, c.StateDir, c.CursorKeyFile = 1, "redis-fixture", "/tmp/redis-state", "/tmp/cursor"
	target := core.Target{Kind: "room", Name: "#fixture"}
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	c.Upstream = &Upstream{Network: "unix", Address: "/tmp/native.sock"}
	c.RedisPubSub = []RedisPubSub{{ID: "redis", Address: "127.0.0.1:6379", TLSServerName: "localhost",
		Subscriber: RedisCredential{Username: "reader", PasswordFile: "/tmp/reader"},
		Publisher:  RedisCredential{Username: "writer", PasswordFile: "/tmp/writer"},
		Routes:     []RedisRoute{{RequestChannel: "airc request.*é", ResponseChannel: "airc response", Target: target, Operations: ops}}}}
	return c
}

func TestRedisPubSubConfiguration(t *testing.T) {
	valid := redisConfig()
	if err := valid.Validate(); err != nil || valid.ResponseSigningKey != nil || len(valid.Principals) != 0 {
		t.Fatal("guest route without principals or response signer", err)
	}
	valid.RedisPubSub[0].Routes[0].BoundAuthor = &BoundAuthor{Nick: "alerts", AccountTokenFile: "/tmp/alerts.token"}
	if err := valid.Validate(); err != nil {
		t.Fatal("bound route", err)
	}
	no := false
	for name, mutate := range map[string]func(*Config){
		"missing_name":      func(c *Config) { c.RedisPubSub[0].TLSServerName = "" },
		"wildcard_name":     func(c *Config) { c.RedisPubSub[0].TLSServerName = "*.example.com" },
		"invalid_name":      func(c *Config) { c.RedisPubSub[0].TLSServerName = "host:6379" },
		"dns_endpoint":      func(c *Config) { c.RedisPubSub[0].Address = "localhost:6379" },
		"zero_port":         func(c *Config) { c.RedisPubSub[0].Address = "127.0.0.1:000" },
		"unspecified":       func(c *Config) { c.RedisPubSub[0].Address = "0.0.0.0:6379" },
		"relative_ca":       func(c *Config) { c.RedisPubSub[0].TLSCAFile = "ca" },
		"missing_password":  func(c *Config) { c.RedisPubSub[0].Subscriber.PasswordFile = "" },
		"relative_password": func(c *Config) { c.RedisPubSub[0].Publisher.PasswordFile = "password" },
		"missing_user":      func(c *Config) { c.RedisPubSub[0].Publisher.Username = "" },
		"control_user":      func(c *Config) { c.RedisPubSub[0].Subscriber.Username = "user\n" },
		"no_routes":         func(c *Config) { c.RedisPubSub[0].Routes = nil },
		"collision": func(c *Config) {
			c.RedisPubSub[0].Routes[0].ResponseChannel = c.RedisPubSub[0].Routes[0].RequestChannel
		},
		"control_channel": func(c *Config) { c.RedisPubSub[0].Routes[0].RequestChannel = "bad\x00" },
		"invalid_utf8":    func(c *Config) { c.RedisPubSub[0].Routes[0].RequestChannel = "\xff" },
		"large_channel":   func(c *Config) { c.RedisPubSub[0].Routes[0].RequestChannel = strings.Repeat("a", 256) },
		"no_cursor":       func(c *Config) { c.CursorKeyFile = "" },
		"unsupported":     func(c *Config) { c.RedisPubSub[0].Routes[0].Operations = []core.Operation{core.Watch} },
		"duplicate_op":    func(c *Config) { c.RedisPubSub[0].Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"empty_op":        func(c *Config) { c.RedisPubSub[0].Routes[0].Operations = nil },
		"no_author":       func(c *Config) { c.RedisPubSub[0].Routes[0].Guests = &no },
		"bound_nick":      func(c *Config) { c.RedisPubSub[0].Routes[0].BoundAuthor = &BoundAuthor{Nick: "has space"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := redisConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted unsafe broker configuration")
			}
		})
	}
}

func TestRedisPubSubBoundsAndNamespaces(t *testing.T) {
	for _, count := range []int{128, 129} {
		c := redisConfig()
		r := c.RedisPubSub[0].Routes[0]
		c.RedisPubSub[0].Routes = nil
		for i := range count {
			r.RequestChannel, r.ResponseChannel = fmt.Sprintf("request%d", i), fmt.Sprintf("response%d", i)
			c.RedisPubSub[0].Routes = append(c.RedisPubSub[0].Routes, r)
		}
		if err := c.Validate(); (err == nil) != (count == 128) {
			t.Fatalf("routes=%d: %v", count, err)
		}
	}
	c := redisConfig()
	c.RedisPubSub[0].Routes[0].Operations = []core.Operation{core.Send}
	c.CursorKeyFile = ""
	c.RedisPubSub[0].Routes[0].RequestChannel = strings.Repeat("é", 127) + "x"
	c.RedisPubSub[0].Address, c.RedisPubSub[0].TLSServerName = "[::1]:6379", "::1"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	b := c.RedisPubSub[0]
	b.ID, b.Address = "second", "[0:0:0:0:0:0:0:1]:06379"
	c.RedisPubSub = append(c.RedisPubSub, b)
	if err := c.Validate(); err == nil {
		t.Fatal("accepted duplicate channels at equivalent endpoint")
	}
	c.RedisPubSub[1].Address = "[::1]:6380"
	if err := c.Validate(); err != nil {
		t.Fatal("distinct broker namespace", err)
	}
	c.RedisPubSub[1].ID = "redis"
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate broker ID")
	}
}

func TestRedisPubSubJSONContract(t *testing.T) {
	wire, err := json.Marshal(redisConfig())
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(wire)
	if err != nil || len(c.RedisPubSub) != 1 || c.RedisPubSub[0].Subscriber.Username != "reader" {
		t.Fatalf("parse: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(string(wire), "password_file", "password", 1),
		strings.Replace(string(wire), "request_channel", "Request_channel", 1),
		strings.Replace(string(wire), `"username":"reader"`, `"username":"reader","username":"other"`, 1),
		strings.Replace(string(wire), `"subscriber":{`, `"subscriber":null,"unknown":{`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("accepted invalid JSON schema")
		}
	}
	old, _ := json.Marshal(mqttConfig())
	if _, err := Parse(old); err != nil {
		t.Fatal("existing schema regressed", err)
	}
}
