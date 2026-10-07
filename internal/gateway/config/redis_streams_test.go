package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func streamsConfig() Config {
	c := redisConfig()
	b := c.RedisPubSub[0]
	c.RedisPubSub = nil
	c.RedisStreams = []RedisStreams{{ID: "streams", Namespace: "broker", Address: b.Address, TLSServerName: b.TLSServerName,
		Credential: b.Subscriber, Group: "gateway", Consumer: "owner", Route: RedisStreamRoute{
			Principal: b.Routes[0].Principal, Stream: "events", ResultStream: "results", Target: b.Routes[0].Target, Operations: b.Routes[0].Operations}}}
	return c
}

func TestRedisStreamsConfiguration(t *testing.T) {
	valid := streamsConfig()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"enabled":       func(c *Config) { c.RedisStreams[0].Enabled = true },
		"namespace":     func(c *Config) { c.RedisStreams[0].Namespace = "" },
		"dial":          func(c *Config) { c.RedisStreams[0].Address = "0.0.0.0:6379" },
		"port":          func(c *Config) { c.RedisStreams[0].Address = "127.0.0.1:0" },
		"name":          func(c *Config) { c.RedisStreams[0].TLSServerName = "" },
		"ca":            func(c *Config) { c.RedisStreams[0].TLSCAFile = "relative" },
		"password":      func(c *Config) { c.RedisStreams[0].Credential.PasswordFile = "relative" },
		"user":          func(c *Config) { c.RedisStreams[0].Credential.Username = "bad\n" },
		"group":         func(c *Config) { c.RedisStreams[0].Group = "\x00" },
		"consumer":      func(c *Config) { c.RedisStreams[0].Consumer = "" },
		"stream":        func(c *Config) { c.RedisStreams[0].Route.Stream = "\xff" },
		"result":        func(c *Config) { c.RedisStreams[0].Route.ResultStream = "events" },
		"oversize":      func(c *Config) { c.RedisStreams[0].Route.Stream = strings.Repeat("x", 256) },
		"principal":     func(c *Config) { c.RedisStreams[0].Route.Principal = "unknown" },
		"revoked":       func(c *Config) { c.Principals[0].Revoked = true },
		"unsigned":      func(c *Config) { c.Principals[0].SigningKeys = nil },
		"target":        func(c *Config) { c.RedisStreams[0].Route.Target.Name = "#other" },
		"empty_ops":     func(c *Config) { c.RedisStreams[0].Route.Operations = nil },
		"duplicate_ops": func(c *Config) { c.RedisStreams[0].Route.Operations = []core.Operation{core.Send, core.Send} },
		"unsupported":   func(c *Config) { c.RedisStreams[0].Route.Operations = []core.Operation{core.Watch} },
		"grant":         func(c *Config) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"cursor":        func(c *Config) { c.CursorKeyFile = "" },
		"signer":        func(c *Config) { c.ResponseSigningKey = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := streamsConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	c := streamsConfig()
	c.RedisStreams[0].Route.Operations = []core.Operation{core.Send}
	c.CursorKeyFile = ""
	c.RedisStreams[0].Route.Stream = "é.* literal"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisStreamsNamespaces(t *testing.T) {
	for _, count := range []int{32, 33} {
		c := streamsConfig()
		b := c.RedisStreams[0]
		c.RedisStreams = nil
		for i := range count {
			b.ID = fmt.Sprintf("owner%d", i)
			b.Route.Stream = fmt.Sprintf("events%d", i)
			b.Route.ResultStream = fmt.Sprintf("results%d", i)
			c.RedisStreams = append(c.RedisStreams, b)
		}
		if err := c.Validate(); (err == nil) != (count == 32) {
			t.Fatalf("count %d: %v", count, err)
		}
	}
	for _, mutate := range []func(*RedisStreams){
		func(b *RedisStreams) { b.ID = "streams" },
		func(b *RedisStreams) { b.Namespace = "different" },
		func(b *RedisStreams) { b.Address = "127.0.0.1:6380" },
		func(b *RedisStreams) { b.Route.Stream = "events" },
		func(b *RedisStreams) { b.Route.Stream = "results" },
	} {
		c := streamsConfig()
		b := c.RedisStreams[0]
		b.ID = "second"
		b.Route.Stream, b.Route.ResultStream = "next", "next-result"
		mutate(&b)
		c.RedisStreams = append(c.RedisStreams, b)
		if err := c.Validate(); err == nil {
			t.Fatal("namespace collision accepted")
		}
	}
	c := streamsConfig()
	b := c.RedisStreams[0]
	b.ID, b.Address, b.Namespace = "second", "127.0.0.1:6380", "different"
	c.RedisStreams = append(c.RedisStreams, b)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisStreamsJSON(t *testing.T) {
	wire, err := json.Marshal(streamsConfig())
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(wire)
	if err != nil || len(c.RedisStreams) != 1 {
		t.Fatalf("parse: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(string(wire), `"stream":`, `"Stream":`, 1),
		strings.Replace(string(wire), `"namespace":"broker"`, `"namespace":"broker","namespace":"other"`, 1),
		strings.Replace(string(wire), `"credential":{`, `"credential":null,"unknown":{`, 1),
		strings.Replace(string(wire), `"route":{`, `"route":null,"unknown":{`, 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}
