package config

import (
	"testing"
)

func multicastIPv6Config() Config {
	c := udpConfig(1)
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
	c.Listeners[0].Protocol = "ipv6_multicast"
	c.Listeners[0].Address = "[ff12::1234]:9001"
	c.Listeners[0].IPv6Multicast = &IPv6Multicast{Interface: "lo0"}
	c.ReturnRoutes[0].Protocol = "ipv6_multicast"
	c.ReturnRoutes[0].Destination = "[::1]:9002"
	return c
}

func TestIPv6MulticastProtectedRoutePrerequisite(t *testing.T) {
	c := multicastIPv6Config()
	if err := c.validateIPv6Multicast(); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil {
		t.Fatal("activated IPv6 multicast before socket ownership")
	}
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal("rejected disabled configuration", err)
	}
}

func TestIPv6MulticastRejectsUnsafeProtectedRoutes(t *testing.T) {
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
			c := multicastIPv6Config()
			mutate(&c)
			if err := c.validateIPv6Multicast(); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
	for _, destination := range []string{"239.255.1.2:9002", "0.0.0.0:9002", "255.255.255.255:9002", "[ff12::1234]:9002", "[::]:9002", "[::ffff:127.0.0.1]:9002", "[fe80::1]:9002", "[fe80::1%other]:9002", "[::1%lo0]:9002", "[::1]:09002", "example.com:9002", "127.0.0.1:0", "127.0.0.1:09002"} {
		t.Run(destination, func(t *testing.T) {
			c := multicastIPv6Config()
			c.ReturnRoutes[0].Destination = destination
			if err := c.validateIPv6Multicast(); err == nil {
				t.Fatal("accepted unsafe destination")
			}
		})
	}
}

func TestIPv6MulticastRoutesRemainScopedToKnownPrincipals(t *testing.T) {
	c := multicastIPv6Config()
	c.Listeners[0].Enabled = false
	c.ReturnRoutes[0].Principal = "unknown"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted unknown route principal")
	}
	c = multicastIPv6Config()
	other := c.Principals[0]
	other.ID = "other"
	c.Principals = append(c.Principals, other)
	if err := c.validateIPv6Multicast(); err == nil {
		t.Fatal("accepted a second principal without its own route")
	}
	route := c.ReturnRoutes[0]
	route.ID, route.Principal = "other-route", "other"
	c.ReturnRoutes = append(c.ReturnRoutes, route)
	if err := c.validateIPv6Multicast(); err != nil {
		t.Fatal("rejected two explicitly routed principals", err)
	}
}

func TestIPv6MulticastLinkLocalRouteZone(t *testing.T) {
	c := multicastIPv6Config()
	c.ReturnRoutes[0].Destination = "[fe80::1%lo0]:9002"
	if err := c.validateIPv6Multicast(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal("rejected disabled scoped route", err)
	}
}
