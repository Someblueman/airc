package config

import (
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func matrixBridgeFixture() (Config, MatrixBridge) {
	c := stompConfig()
	b := MatrixBridge{ID: "matrix", Address: "127.0.0.1:8448", TLSServerName: "matrix.example.com", Account: "@bridge:example.com", AccessTokenFile: "/private/matrix-token", FreshnessSeconds: 60,
		Accounts: []BridgeAccount{{External: "@carol:elsewhere.org", Nick: "carol", AccountTokenFile: "/private/carol.token"}},
		Routes:   []MatrixRoute{{Room: "!OpaqueRoom", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}}}
	return c, b
}

func TestMatrixProvisioning(t *testing.T) {
	c, b := matrixBridgeFixture()
	if err := c.ValidateMatrixBridge(b); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config, *MatrixBridge){
		"dns dial":            func(c *Config, b *MatrixBridge) { b.Address = "matrix.example.com:8448" },
		"wildcard":            func(c *Config, b *MatrixBridge) { b.Address = "0.0.0.0:8448" },
		"zero port":           func(c *Config, b *MatrixBridge) { b.Address = "127.0.0.1:0" },
		"TLS name":            func(c *Config, b *MatrixBridge) { b.TLSServerName = "" },
		"CA path":             func(c *Config, b *MatrixBridge) { b.TLSCAFile = "relative" },
		"token path":          func(c *Config, b *MatrixBridge) { b.AccessTokenFile = "relative" },
		"account":             func(c *Config, b *MatrixBridge) { b.Account = "bridge" },
		"room alias":          func(c *Config, b *MatrixBridge) { b.Routes[0].Room = "#room:example.com" },
		"duplicate":           func(c *Config, b *MatrixBridge) { b.Routes = append(b.Routes, b.Routes[0]) },
		"target":              func(c *Config, b *MatrixBridge) { b.Routes[0].Target.Name = "#Not Canonical" },
		"no operations":       func(c *Config, b *MatrixBridge) { b.Routes[0].Operations = nil },
		"duplicate operation": func(c *Config, b *MatrixBridge) { b.Routes[0].Operations = []core.Operation{core.Send, core.Send} },
		"streaming operation": func(c *Config, b *MatrixBridge) { b.Routes[0].Operations = []core.Operation{core.Watch} },
		"cursor":              func(c *Config, b *MatrixBridge) { c.CursorKeyFile = "" },
		"mapped user":         func(c *Config, b *MatrixBridge) { b.Accounts[0].External = "carol" },
		"mapped self":         func(c *Config, b *MatrixBridge) { b.Accounts[0].External = b.Account },
		"mapped nick":         func(c *Config, b *MatrixBridge) { b.Accounts[0].Nick = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c, b := matrixBridgeFixture()
			mutate(&c, &b)
			if c.ValidateMatrixBridge(b) == nil {
				t.Fatal("invalid provisioning accepted")
			}
		})
	}
}

func TestMatrixProvisioningBoundsAndExactRoom(t *testing.T) {
	for _, n := range []int{0, 1, 128, 129} {
		c, b := matrixBridgeFixture()
		r := b.Routes[0]
		b.Routes = nil
		for i := range n {
			r.Room = "!room" + strconv.Itoa(i)
			b.Routes = append(b.Routes, r)
		}
		if err := c.ValidateMatrixBridge(b); (err == nil) != (n >= 1 && n <= 128) {
			t.Fatal(n, err)
		}
	}
	for _, n := range []int64{0, 1, 300, 301} {
		c, b := matrixBridgeFixture()
		b.FreshnessSeconds = n
		if err := c.ValidateMatrixBridge(b); (err == nil) != (n == 1 || n == 300) {
			t.Fatal(n, err)
		}
	}
	c, b := matrixBridgeFixture()
	r := b.Routes[0]
	r.Room = "!opaqueroom"
	b.Routes = append(b.Routes, r)
	if err := c.ValidateMatrixBridge(b); err != nil {
		t.Fatal("case-distinct rooms conflated", err)
	}
}
