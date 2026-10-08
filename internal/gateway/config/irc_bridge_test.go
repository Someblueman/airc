package config

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func ircBridgeFixture() (Config, IRCBridge) {
	c := stompConfig()
	b := IRCBridge{ID: "network", Address: "127.0.0.1:6697", TLSServerName: "irc.example.com", Nickname: "airc-bridge", Account: "airc-bridge", PasswordFile: "/private/irc-password", FreshnessSeconds: 60,
		Routes: []IRCRoute{{Account: "alice", Room: "#external", Principal: c.Principals[0].ID, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}}}
	return c, b
}

func TestIRCBridgeProvisioning(t *testing.T) {
	c, b := ircBridgeFixture()
	if err := c.ValidateIRCBridge(b); err != nil {
		t.Fatal(err)
	}
	b.Address = "[::1]:6697"
	b.TLSServerName = "::1"
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
		"self_loop":      func(c *Config, b *IRCBridge) { b.Routes[0].Account = strings.ToUpper(b.Account) },
		"wildcard":       func(c *Config, b *IRCBridge) { b.Routes[0].Room = "#*" },
		"targets":        func(c *Config, b *IRCBridge) { b.Routes[0].Room = "#a,#b" },
		"unknown":        func(c *Config, b *IRCBridge) { b.Routes[0].Principal = "unknown" },
		"revoked":        func(c *Config, b *IRCBridge) { c.Principals[0].Revoked = true },
		"native_target":  func(c *Config, b *IRCBridge) { b.Routes[0].Target.Name = "#other" },
		"operations":     func(c *Config, b *IRCBridge) { b.Routes[0].Operations = nil },
		"watch":          func(c *Config, b *IRCBridge) { b.Routes[0].Operations = []core.Operation{core.Watch} },
		"duplicate_op":   func(c *Config, b *IRCBridge) { b.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"authority":      func(c *Config, b *IRCBridge) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"cursor":         func(c *Config, b *IRCBridge) { c.CursorKeyFile = "" },
		"case_collision": func(c *Config, b *IRCBridge) {
			r := b.Routes[0]
			r.Account = "ALICE"
			r.Room = "#EXTERNAL"
			b.Routes = append(b.Routes, r)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, b := ircBridgeFixture()
			mutate(&c, &b)
			if err := c.ValidateIRCBridge(b); err == nil {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
}

func TestIRCGrantBoundsAndLiteralNames(t *testing.T) {
	for _, n := range []int{128, 129} {
		c, b := ircBridgeFixture()
		r := b.Routes[0]
		b.Routes = nil
		for i := range n {
			r.Account = strings.Repeat("a", i/64+1) + string(rune('A'+i%26)) + string(rune('0'+i/26))
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
