package config

import (
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func xmppBridgeFixture() (Config, XMPPBridge) {
	c := stompConfig()
	b := XMPPBridge{ID: "xmpp", Address: "127.0.0.1:5223", TLSServerName: "xmpp.example.com", Account: "bridge@example.com", Resource: "airc", PasswordFile: "/private/xmpp-password", FreshnessSeconds: 60,
		Accounts: []BridgeAccount{{External: "carol@example.com", Nick: "carol", AccountTokenFile: "/private/carol.token"}},
		Routes:   []XMPPRoute{{Room: "room@muc.example.com", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}}}
	return c, b
}

func TestXMPPRouteAndFreshnessBounds(t *testing.T) {
	for _, n := range []int{128, 129} {
		c, b := xmppBridgeFixture()
		r := b.Routes[0]
		b.Routes = nil
		for i := range n {
			r.Room = "room" + strconv.Itoa(i) + "@muc.example.com"
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

func TestXMPPProvisioning(t *testing.T) {
	c, b := xmppBridgeFixture()
	if err := c.ValidateXMPPBridge(b); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config, *XMPPBridge){
		"endpoint":     func(c *Config, b *XMPPBridge) { b.Address = "xmpp.example.com:5223" },
		"wildcard":     func(c *Config, b *XMPPBridge) { b.Address = "0.0.0.0:5223" },
		"zeroport":     func(c *Config, b *XMPPBridge) { b.Address = "127.0.0.1:0" },
		"tls":          func(c *Config, b *XMPPBridge) { b.TLSServerName = "*.example.com" },
		"ca":           func(c *Config, b *XMPPBridge) { b.TLSCAFile = "relative" },
		"credential":   func(c *Config, b *XMPPBridge) { b.PasswordFile = "relative" },
		"account":      func(c *Config, b *XMPPBridge) { b.Account = "Bridge@example.com" },
		"resource":     func(c *Config, b *XMPPBridge) { b.Resource = "" },
		"freshness":    func(c *Config, b *XMPPBridge) { b.FreshnessSeconds = 301 },
		"routes":       func(c *Config, b *XMPPBridge) { b.Routes = nil },
		"duplicate":    func(c *Config, b *XMPPBridge) { b.Routes = append(b.Routes, b.Routes[0]) },
		"room":         func(c *Config, b *XMPPBridge) { b.Routes[0].Room = "room@muc.example.com/nick" },
		"target":       func(c *Config, b *XMPPBridge) { b.Routes[0].Target.Name = "#Not Canonical" },
		"ops":          func(c *Config, b *XMPPBridge) { b.Routes[0].Operations = nil },
		"duplicateop":  func(c *Config, b *XMPPBridge) { b.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"watch":        func(c *Config, b *XMPPBridge) { b.Routes[0].Operations = []core.Operation{core.Watch} },
		"cursor":       func(c *Config, b *XMPPBridge) { c.CursorKeyFile = "" },
		"self_mapped":  func(c *Config, b *XMPPBridge) { b.Accounts[0].External = b.Account },
		"full_jid":     func(c *Config, b *XMPPBridge) { b.Accounts[0].External = "carol@example.com/phone" },
		"mapped_nick":  func(c *Config, b *XMPPBridge) { b.Accounts[0].Nick = "carol@example.com" },
		"mapped_token": func(c *Config, b *XMPPBridge) { b.Accounts[0].AccountTokenFile = "" },
		"prefix":       func(c *Config, b *XMPPBridge) { b.GuestPrefix = "-x" },
	} {
		t.Run(name, func(t *testing.T) {
			c, b := xmppBridgeFixture()
			mutate(&c, &b)
			if err := c.ValidateXMPPBridge(b); err == nil {
				t.Fatal("unsafe bridge accepted")
			}
		})
	}
}
