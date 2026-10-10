package config

import (
	"runtime"
	"testing"
)

func broadcastIPv4Config() Config {
	c := udpConfig(1)
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
	c.Listeners[0].Protocol = "ipv4_broadcast"
	c.Listeners[0].Address = "192.0.2.255:9001"
	c.Listeners[0].IPv4Broadcast = &IPv4Broadcast{Interface: "lo0", Subnet: "192.0.2.0/24"}
	c.ReturnRoutes[0].Protocol = "ipv4_broadcast"
	return c
}

func TestIPv4BroadcastProtectedRoutePrerequisite(t *testing.T) {
	c := broadcastIPv4Config()
	if err := c.validateIPv4Broadcast(); err != nil {
		t.Fatal(err)
	}
	err := c.Validate()
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if err != nil {
			t.Fatalf("rejected enabled IPv4 broadcast config on a supported platform: %v", err)
		}
	} else if err == nil {
		t.Fatal("accepted enabled IPv4 broadcast outside supported platforms")
	}
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal("rejected disabled configuration", err)
	}
}

func TestIPv4BroadcastRejectsUnsafeProtectedRoutes(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"missing route":          func(c *Config) { c.ReturnRoutes = nil },
		"duplicate route":        func(c *Config) { c.ReturnRoutes = append(c.ReturnRoutes, c.ReturnRoutes[0]) },
		"unknown listener":       func(c *Config) { c.ReturnRoutes[0].Listener = "other" },
		"missing recipient":      func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" },
		"relative key":           func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "key" },
		"missing signer":         func(c *Config) { c.ResponseSigningKey = nil },
		"missing ingress key":    func(c *Config) { c.RecipientKeys = nil },
		"missing principal":      func(c *Config) { c.Principals = nil },
		"bearer only":            func(c *Config) { c.Principals[0].SigningKeys = nil },
		"TLS":                    func(c *Config) { c.Listeners[0].TLSCertFile = "/private/cert" },
		"origins":                func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"wrong options protocol": func(c *Config) { c.Listeners[0].Protocol = "udp" },
	} {
		t.Run(name, func(t *testing.T) {
			c := broadcastIPv4Config()
			mutate(&c)
			if err := c.validateIPv4Broadcast(); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
	for _, destination := range []string{"239.255.1.2:9002", "192.0.2.255:9002", "192.0.2.0:9002", "0.0.0.0:9002", "255.255.255.255:9002", "[::1]:9002", "example.com:9002", "127.0.0.1:0", "127.0.0.1:09002"} {
		t.Run(destination, func(t *testing.T) {
			c := broadcastIPv4Config()
			c.ReturnRoutes[0].Destination = destination
			if err := c.validateIPv4Broadcast(); err == nil {
				t.Fatal("accepted unsafe destination")
			}
		})
	}
}

func TestIPv4BroadcastRoutesRemainScopedToKnownPrincipals(t *testing.T) {
	c := broadcastIPv4Config()
	c.Listeners[0].Enabled = false
	c.ReturnRoutes[0].Principal = "unknown"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted unknown route principal")
	}
	c = broadcastIPv4Config()
	other := c.Principals[0]
	other.ID = "other"
	c.Principals = append(c.Principals, other)
	if err := c.validateIPv4Broadcast(); err == nil {
		t.Fatal("accepted a second principal without its own route")
	}
	route := c.ReturnRoutes[0]
	route.ID, route.Principal = "other-route", "other"
	c.ReturnRoutes = append(c.ReturnRoutes, route)
	if err := c.validateIPv4Broadcast(); err != nil {
		t.Fatal("rejected two explicitly routed principals", err)
	}
}
