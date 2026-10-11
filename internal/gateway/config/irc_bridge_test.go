package config

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func ircBridgeFixture() (Config, IRCBridge) {
	c := stompConfig()
	b := IRCBridge{ID: "network", Address: "127.0.0.1:6697", TLSServerName: "irc.example.com", Nickname: "airc-bridge", Account: "airc-bridge", PasswordFile: "/private/irc-password", FreshnessSeconds: 60,
		Accounts: []BridgeAccount{{External: "alice", Nick: "alice", AccountTokenFile: "/private/alice.token"}},
		Routes:   []IRCRoute{{Room: "#external", Target: fixtureRoom, Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}}}
	return c, b
}

func TestIRCBridgeProvisioning(t *testing.T) {
	c, b := ircBridgeFixture()
	if err := c.ValidateIRCBridge(b); err != nil {
		t.Fatal(err)
	}
	b.Address = "[::1]:6697"
	b.TLSServerName = "::1"
	b.GuestPrefix = "ircnet_"
	b.Accounts = nil
	if err := c.ValidateIRCBridge(b); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config, *IRCBridge){
		"id":             func(c *Config, b *IRCBridge) { b.ID = "" },
		"dns_endpoint":   func(c *Config, b *IRCBridge) { b.Address = "irc.example.com:6697" },
		"zero_port":      func(c *Config, b *IRCBridge) { b.Address = "127.0.0.1:0" },
		"unspecified":    func(c *Config, b *IRCBridge) { b.Address = "0.0.0.0:6697" },
		"tls_name":       func(c *Config, b *IRCBridge) { b.TLSServerName = "*.example.com" },
		"ca":             func(c *Config, b *IRCBridge) { b.TLSCAFile = "relative" },
		"password":       func(c *Config, b *IRCBridge) { b.PasswordFile = "relative" },
		"nick":           func(c *Config, b *IRCBridge) { b.Nickname = "0invalid" },
		"account":        func(c *Config, b *IRCBridge) { b.Account = "invalid\r\n" },
		"freshness_zero": func(c *Config, b *IRCBridge) { b.FreshnessSeconds = 0 },
		"freshness_max":  func(c *Config, b *IRCBridge) { b.FreshnessSeconds = 301 },
		"routes":         func(c *Config, b *IRCBridge) { b.Routes = nil },
		"wildcard":       func(c *Config, b *IRCBridge) { b.Routes[0].Room = "#*" },
		"targets":        func(c *Config, b *IRCBridge) { b.Routes[0].Room = "#a,#b" },
		"target":         func(c *Config, b *IRCBridge) { b.Routes[0].Target.Name = "#Not Canonical" },
		"operations":     func(c *Config, b *IRCBridge) { b.Routes[0].Operations = nil },
		"watch":          func(c *Config, b *IRCBridge) { b.Routes[0].Operations = []core.Operation{core.Watch} },
		"duplicate_op":   func(c *Config, b *IRCBridge) { b.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"cursor":         func(c *Config, b *IRCBridge) { c.CursorKeyFile = "" },
		"prefix_digit":   func(c *Config, b *IRCBridge) { b.GuestPrefix = "0irc-" },
		"prefix_long":    func(c *Config, b *IRCBridge) { b.GuestPrefix = strings.Repeat("a", 17) },
		"prefix_special": func(c *Config, b *IRCBridge) { b.GuestPrefix = "irc|" },
		"self_mapped":    func(c *Config, b *IRCBridge) { b.Accounts[0].External = strings.ToUpper(b.Account) },
		"mapped_nick":    func(c *Config, b *IRCBridge) { b.Accounts[0].Nick = "0alice" },
		"mapped_token":   func(c *Config, b *IRCBridge) { b.Accounts[0].AccountTokenFile = "relative" },
		"mapped_account": func(c *Config, b *IRCBridge) { b.Accounts[0].External = "a b" },
		"mapped_twice": func(c *Config, b *IRCBridge) {
			b.Accounts = append(b.Accounts, BridgeAccount{External: "ALICE", Nick: "other", AccountTokenFile: "/private/other.token"})
		},
		"room_case_collision": func(c *Config, b *IRCBridge) {
			r := b.Routes[0]
			r.Room = "#EXTERNAL"
			b.Routes = append(b.Routes, r)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, b := ircBridgeFixture()
			mutate(&c, &b)
			if err := c.ValidateIRCBridge(b); err == nil {
				t.Fatal("unsafe bridge accepted")
			}
		})
	}
}

func TestIRCRouteBoundsAndLiteralNames(t *testing.T) {
	for _, n := range []int{128, 129} {
		c, b := ircBridgeFixture()
		r := b.Routes[0]
		b.Routes = nil
		for i := range n {
			r.Room = "#r" + strings.Repeat("a", i/26) + string(rune('a'+i%26))
			b.Routes = append(b.Routes, r)
		}
		if err := c.ValidateIRCBridge(b); (err == nil) != (n == 128) {
			t.Fatal(n, err)
		}
	}
	for _, s := range []string{"alice", "ALICE", "a._-0", strings.Repeat("a", 64)} {
		if !IRCAccount(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "*", "nick!user@host", "a b", "世界", strings.Repeat("a", 65)} {
		if IRCAccount(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"#a", "#A-0_", "#" + strings.Repeat("a", 63)} {
		if !IRCRoom(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"#", "&room", "@#room", "#a,b", "#a[b]", "#a b"} {
		if IRCRoom(s) {
			t.Fatal(s)
		}
	}
}
