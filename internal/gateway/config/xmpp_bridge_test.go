package config

import (
	"github.com/Someblueman/airc/internal/gateway/core"
	"strconv"
	"testing"
)

func xmppBridgeFixture() (Config, XMPPBridge) {
	c := stompConfig()
	b := XMPPBridge{ID: "xmpp", Address: "127.0.0.1:5223", TLSServerName: "xmpp.example.com", Account: "bridge@example.com", Resource: "airc", PasswordFile: "/private/xmpp-password", FreshnessSeconds: 60,
		Routes: []XMPPRoute{{Author: "alice@example.com", Room: "room@muc.example.com", Principal: c.Principals[0].ID, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}}}
	return c, b
}

func TestXMPPGrantAndFreshnessBounds(t *testing.T) {
	for _, n := range []int{128, 129} {
		c, b := xmppBridgeFixture()
		r := b.Routes[0]
		b.Routes = nil
		for i := range n {
			r.Author = "author" + strconv.Itoa(i) + "@example.com"
			b.Routes = append(b.Routes, r)
		}
		if err := c.ValidateXMPPBridge(b); (err == nil) != (n == 128) {
			t.Fatal(n, err)
		}
	}
	for _, n := range []int64{0, 1, 300, 301} {
		c, b := xmppBridgeFixture()
		b.FreshnessSeconds = n
		if err := c.ValidateXMPPBridge(b); (err == nil) != (n == 1 || n == 300) {
			t.Fatal(n, err)
		}
	}
}

func TestXMPPProvisionedAuthority(t *testing.T) {
	c, b := xmppBridgeFixture()
	if err := c.ValidateXMPPBridge(b); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config, *XMPPBridge){
		"endpoint":    func(c *Config, b *XMPPBridge) { b.Address = "xmpp.example.com:5223" },
		"wildcard":    func(c *Config, b *XMPPBridge) { b.Address = "0.0.0.0:5223" },
		"zeroport":    func(c *Config, b *XMPPBridge) { b.Address = "127.0.0.1:0" },
		"tls":         func(c *Config, b *XMPPBridge) { b.TLSServerName = "*.example.com" },
		"ca":          func(c *Config, b *XMPPBridge) { b.TLSCAFile = "relative" },
		"credential":  func(c *Config, b *XMPPBridge) { b.PasswordFile = "relative" },
		"account":     func(c *Config, b *XMPPBridge) { b.Account = "Bridge@example.com" },
		"resource":    func(c *Config, b *XMPPBridge) { b.Resource = "" },
		"freshness":   func(c *Config, b *XMPPBridge) { b.FreshnessSeconds = 301 },
		"routes":      func(c *Config, b *XMPPBridge) { b.Routes = nil },
		"duplicate":   func(c *Config, b *XMPPBridge) { b.Routes = append(b.Routes, b.Routes[0]) },
		"loop":        func(c *Config, b *XMPPBridge) { b.Routes[0].Author = b.Account },
		"room":        func(c *Config, b *XMPPBridge) { b.Routes[0].Room = "room@muc.example.com/nick" },
		"author":      func(c *Config, b *XMPPBridge) { b.Routes[0].Author = "alice@example.com/resource" },
		"principal":   func(c *Config, b *XMPPBridge) { b.Routes[0].Principal = "unknown" },
		"revoked":     func(c *Config, b *XMPPBridge) { c.Principals[0].Revoked = true },
		"target":      func(c *Config, b *XMPPBridge) { b.Routes[0].Target.Name = "#other" },
		"ops":         func(c *Config, b *XMPPBridge) { b.Routes[0].Operations = nil },
		"duplicateop": func(c *Config, b *XMPPBridge) { b.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"authority":   func(c *Config, b *XMPPBridge) { c.Principals[0].Operations = []core.Operation{core.Send} },
		"watch":       func(c *Config, b *XMPPBridge) { b.Routes[0].Operations = []core.Operation{core.Watch} },
		"cursor":      func(c *Config, b *XMPPBridge) { c.CursorKeyFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c, b := xmppBridgeFixture()
			mutate(&c, &b)
			if err := c.ValidateXMPPBridge(b); err == nil {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
	// Distinct author domains are distinct grants, even with the same localpart.
	r := b.Routes[0]
	r.Author = "alice@other.example.com"
	b.Routes = append(b.Routes, r)
	if err := c.ValidateXMPPBridge(b); err != nil {
		t.Fatal(err)
	}
}
