package config

import (
	"testing"
)

func multicastIPv4Config() Config {
	c := udpConfig(1)
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
	c.Listeners[0].Protocol = "ipv4_multicast"
	c.Listeners[0].Address = "239.255.1.2:9001"
	c.Listeners[0].IPv4Multicast = &IPv4Multicast{Interface: "lo0"}
	c.ReturnRoutes[0].Protocol = "ipv4_multicast"
	return c
}

func TestIPv4MulticastProtectedRoutePrerequisite(t *testing.T) {
	c := multicastIPv4Config()
	if err := c.validateIPv4Multicast(); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal("rejected protected multicast configuration", err)
	}
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal("rejected disabled configuration", err)
	}
}

func TestIPv4MulticastRejectsUnsafeProtectedRoutes(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
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
			c := multicastIPv4Config()
			mutate(&c)
			if err := c.validateIPv4Multicast(); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
	for _, destination := range []string{"239.255.1.2:9002", "0.0.0.0:9002", "255.255.255.255:9002", "[::1]:9002", "example.com:9002", "127.0.0.1:0", "127.0.0.1:09002"} {
		t.Run(destination, func(t *testing.T) {
			c := multicastIPv4Config()
			c.ReturnRoutes[0].Destination = destination
			if err := c.validateIPv4Multicast(); err == nil {
				t.Fatal("accepted unsafe destination")
			}
		})
	}
}

func TestIPv4MulticastRoutesRemainScopedToKnownPrincipals(t *testing.T) {
	c := multicastIPv4Config()
	c.Listeners[0].Enabled = false
	c.ReturnRoutes[0].Principal = "unknown"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted unknown route principal")
	}
	c = multicastIPv4Config()
	other := c.Principals[0]
	other.ID = "other"
	c.Principals = append(c.Principals, other)
	if err := c.validateIPv4Multicast(); err == nil {
		t.Fatal("accepted a second principal without its own route")
	}
	route := c.ReturnRoutes[0]
	route.ID, route.Principal = "other-route", "other"
	c.ReturnRoutes = append(c.ReturnRoutes, route)
	if err := c.validateIPv4Multicast(); err != nil {
		t.Fatal("rejected two explicitly routed principals", err)
	}
}
