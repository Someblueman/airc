package config

import (
	"encoding/json"
	"slices"
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
	if err != nil || len(parsed.IRCBridges) != 1 || !parsed.IRCBridges[0].Enabled || parsed.IRCBridges[0].ID != b.ID || len(parsed.IRCBridges[0].Accounts) != 1 {
		t.Fatal("IRC configuration", parsed, err)
	}
	for _, kind := range []string{"id", "endpoint", "count", "same_prefix", "nested_prefix", "mapped_prefix"} {
		t.Run(kind, func(t *testing.T) {
			c, b := ircBridgeFixture()
			b.Enabled = true
			b.Routes[0].Operations = []core.Operation{core.Send}
			c.IRCBridges = []IRCBridge{b}
			other := b
			other.ID, other.Address, other.GuestPrefix = "other", "127.0.0.1:6698", "net2-"
			other.Accounts = slices.Clone(b.Accounts)
			switch kind {
			case "id":
				other.ID = b.ID
				c.IRCBridges = append(c.IRCBridges, other)
			case "endpoint":
				other.Address = b.Address
				c.IRCBridges = append(c.IRCBridges, other)
			case "count":
				c.IRCBridges = make([]IRCBridge, 33)
			case "same_prefix":
				// Two bridges with the default prefix would give one AIRC
				// guest to two different networks' users.
				other.GuestPrefix = ""
				c.IRCBridges = append(c.IRCBridges, other)
			case "nested_prefix":
				other.GuestPrefix = "IRC-x"
				c.IRCBridges = append(c.IRCBridges, other)
			case "mapped_prefix":
				c.IRCBridges[0].Accounts[0].Nick = "Irc-alice"
			}
			if err := c.Validate(); err == nil {
				t.Fatal("invalid service profile", kind)
			}
			if kind == "count" {
				return
			}
			// The same profile is valid once the conflict is resolved.
			c, b = ircBridgeFixture()
			b.Enabled = true
			b.Routes[0].Operations = []core.Operation{core.Send}
			c.IRCBridges = []IRCBridge{b, other}
			c.IRCBridges[1].ID, c.IRCBridges[1].Address, c.IRCBridges[1].GuestPrefix = "other", "127.0.0.1:6698", "net2-"
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
