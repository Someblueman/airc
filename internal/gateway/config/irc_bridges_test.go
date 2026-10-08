package config

import (
	"encoding/json"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestIRCServiceCollectionAndPublishedConfiguration(t *testing.T) {
	c, b := ircBridgeFixture()
	b.Enabled = true
	b.Routes[0].Operations = []core.Operation{core.Send}
	c.IRCBridges = []IRCBridge{b}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil || len(parsed.IRCBridges) != 1 || !parsed.IRCBridges[0].Enabled || parsed.IRCBridges[0].ID != b.ID {
		t.Fatal("IRC configuration", parsed, err)
	}
	for _, kind := range []string{"id", "endpoint", "count", "read"} {
		t.Run(kind, func(t *testing.T) {
			c, b := ircBridgeFixture()
			b.Enabled = true
			b.Routes[0].Operations = []core.Operation{core.Send}
			c.IRCBridges = []IRCBridge{b}
			switch kind {
			case "id":
				other := b
				other.Address = "127.0.0.1:6698"
				c.IRCBridges = append(c.IRCBridges, other)
			case "endpoint":
				other := b
				other.ID = "other"
				c.IRCBridges = append(c.IRCBridges, other)
			case "count":
				c.IRCBridges = make([]IRCBridge, 33)
			case "read":
				c.IRCBridges[0].Routes[0].Operations = []core.Operation{core.ContextRead}
			}
			if err := c.Validate(); err == nil {
				t.Fatal("invalid service profile", kind)
			}
		})
	}
}
