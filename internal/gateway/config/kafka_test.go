package config

import (
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func kafkaConfig() Config {
	c := redisConfig()
	r := c.RedisPubSub[0].Routes[0]
	c.RedisPubSub = nil
	initial := int64(0)
	c.Kafka = []Kafka{{ID: "kafka", Namespace: "history", ClusterID: "cluster", Brokers: []KafkaBroker{{ID: 0, Address: "127.0.0.1:9093", TLSServerName: "localhost"}}, Credential: KafkaCredential{Username: "producer", PasswordFile: "/tmp/kafka-password"}, Route: KafkaRoute{Principal: r.Principal, Topic: "events", Partition: 0, InitialOffset: &initial, ResultTopic: "results", ResultPartition: 0, Target: r.Target, Operations: r.Operations}}}
	return c
}

func TestKafkaConfiguration(t *testing.T) {
	valid := kafkaConfig()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"enabled":          func(c *Config) { c.Kafka[0].Enabled = true },
		"id":               func(c *Config) { c.Kafka[0].ID = "" },
		"namespace":        func(c *Config) { c.Kafka[0].Namespace = "" },
		"cluster":          func(c *Config) { c.Kafka[0].ClusterID = "bad\x00" },
		"brokers":          func(c *Config) { c.Kafka[0].Brokers = nil },
		"broker_id":        func(c *Config) { c.Kafka[0].Brokers[0].ID = -1 },
		"dns":              func(c *Config) { c.Kafka[0].Brokers[0].Address = "localhost:9093" },
		"unspecified":      func(c *Config) { c.Kafka[0].Brokers[0].Address = "0.0.0.0:9093" },
		"port":             func(c *Config) { c.Kafka[0].Brokers[0].Address = "127.0.0.1:0" },
		"name":             func(c *Config) { c.Kafka[0].Brokers[0].TLSServerName = "*.example" },
		"ca":               func(c *Config) { c.Kafka[0].Brokers[0].TLSCAFile = "relative" },
		"username":         func(c *Config) { c.Kafka[0].Credential.Username = "bad\n" },
		"password":         func(c *Config) { c.Kafka[0].Credential.PasswordFile = "relative" },
		"initial_missing":  func(c *Config) { c.Kafka[0].Route.InitialOffset = nil },
		"initial_negative": func(c *Config) { *c.Kafka[0].Route.InitialOffset = -1 },
		"initial_overflow": func(c *Config) { *c.Kafka[0].Route.InitialOffset = math.MaxInt64 },
		"partition":        func(c *Config) { c.Kafka[0].Route.Partition = -1 },
		"result_partition": func(c *Config) { c.Kafka[0].Route.ResultPartition = -1 },
		"topic":            func(c *Config) { c.Kafka[0].Route.Topic = ".." },
		"result":           func(c *Config) { c.Kafka[0].Route.ResultTopic = "events" },
		"principal":        func(c *Config) { c.Kafka[0].Route.Principal = "unknown" },
		"revoked":          func(c *Config) { c.Principals[0].Revoked = true },
		"signing":          func(c *Config) { c.Principals[0].SigningKeys = nil },
		"target":           func(c *Config) { c.Kafka[0].Route.Target.Name = "#other" },
		"operations":       func(c *Config) { c.Kafka[0].Route.Operations = nil },
		"unsupported":      func(c *Config) { c.Kafka[0].Route.Operations = []core.Operation{core.Watch} },
		"duplicate":        func(c *Config) { c.Kafka[0].Route.Operations = []core.Operation{core.Send, core.Send} },
		"authority":        func(c *Config) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"cursor":           func(c *Config) { c.CursorKeyFile = "" },
		"signer":           func(c *Config) { c.ResponseSigningKey = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := kafkaConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	c := kafkaConfig()
	c.Kafka[0].Route.Operations = []core.Operation{core.Send}
	c.CursorKeyFile = ""
	c.Kafka[0].Brokers[0].Address = "[::1]:9093"
	c.Kafka[0].Brokers[0].TLSServerName = "::1"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(kafkaConfig())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Parse(wire)
	if err != nil || len(decoded.Kafka) != 1 || decoded.Kafka[0].Route.InitialOffset == nil {
		t.Fatalf("parse %v", err)
	}
}

func TestKafkaOwnership(t *testing.T) {
	for _, mutate := range []func(*Kafka){
		func(b *Kafka) { b.ID = "kafka" },
		func(b *Kafka) { b.Route.Topic = "events" },
		func(b *Kafka) { b.Route.Topic = "results" },
		func(b *Kafka) { b.Namespace = "different"; b.Route.Topic = "events" },
		func(b *Kafka) { b.ClusterID = "other" },
		func(b *Kafka) { b.Brokers[0].TLSServerName = "other.example" },
		func(b *Kafka) { b.Brokers[0].ID = 1 },
		func(b *Kafka) { b.Brokers[0].Address = "127.0.0.1:9094" },
	} {
		c := kafkaConfig()
		b := c.Kafka[0]
		b.Brokers = slices.Clone(b.Brokers)
		b.ID = "second"
		b.Route.Topic = "next"
		b.Route.ResultTopic = "next-result"
		mutate(&b)
		c.Kafka = append(c.Kafka, b)
		if c.Validate() == nil {
			t.Fatal("collision accepted")
		}
	}
	c := kafkaConfig()
	b := c.Kafka[0]
	b.ID = "second"
	b.Route.Partition = 1
	b.Route.ResultPartition = 1
	c.Kafka = append(c.Kafka, b)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, duplicateID := range []bool{false, true} {
		c := kafkaConfig()
		b := c.Kafka[0].Brokers[0]
		if duplicateID {
			b.Address = "127.0.0.1:9094"
		} else {
			b.ID = 1
		}
		c.Kafka[0].Brokers = append(c.Kafka[0].Brokers, b)
		if c.Validate() == nil {
			t.Fatal("duplicate broker accepted")
		}
	}
}
