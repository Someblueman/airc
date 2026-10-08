package config

import (
	"runtime"
	"testing"
)

func udpLiteConfig() Config {
	c := udpConfig(1)
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
	c.Listeners[0].Protocol = "udp_lite"
	c.Listeners[0].Address = "127.0.0.1:9001"
	c.Listeners[0].UDPLite = &UDPLite{ChecksumCoverage: "full"}
	c.ReturnRoutes[0].Protocol = "udp_lite"
	return c
}

func TestUDPLiteProtectedRoutePrerequisite(t *testing.T) {
	c := udpLiteConfig()
	c.Listeners[0].Enabled = runtime.GOOS == "linux"
	if err := c.validateUDPLite(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); (err == nil) != (runtime.GOOS == "linux") {
		t.Fatal("incorrect UDP-Lite platform gate", err)
	}
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatal("rejected disabled configuration", err)
	}
}

func TestUDPLiteRejectsUnsafeProtectedRoutes(t *testing.T) {
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
			c := udpLiteConfig()
			mutate(&c)
			if err := c.validateUDPLite(); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
	for _, destination := range []string{"239.255.1.2:9002", "0.0.0.0:9002", "255.255.255.255:9002", "[::1]:9002", "example.com:9002", "127.0.0.1:0", "127.0.0.1:09002"} {
		t.Run(destination, func(t *testing.T) {
			c := udpLiteConfig()
			c.ReturnRoutes[0].Destination = destination
			if err := c.validateUDPLite(); err == nil {
				t.Fatal("accepted unsafe destination")
			}
		})
	}
}

func TestUDPLiteRoutesRemainScopedToKnownPrincipals(t *testing.T) {
	c := udpLiteConfig()
	c.Listeners[0].Enabled = false
	c.ReturnRoutes[0].Principal = "unknown"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted unknown route principal")
	}
	c = udpLiteConfig()
	other := c.Principals[0]
	other.ID = "other"
	c.Principals = append(c.Principals, other)
	if err := c.validateUDPLite(); err == nil {
		t.Fatal("accepted a second principal without its own route")
	}
	route := c.ReturnRoutes[0]
	route.ID, route.Principal = "other-route", "other"
	c.ReturnRoutes = append(c.ReturnRoutes, route)
	c.Listeners[0].Enabled = runtime.GOOS == "linux"
	if err := c.validateUDPLite(); err != nil {
		t.Fatal("rejected two explicitly routed principals", err)
	}
}

func TestUDPLiteIPv6ReturnRouteScope(t *testing.T) {
	c := udpLiteConfig()
	c.Listeners[0].Enabled = false
	c.Listeners[0].Address = "[fe80::1%lo0]:9001"
	c.ReturnRoutes[0].Destination = "[fe80::2%lo0]:9002"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"[fe80::2%other]:9002", "[::]:9002", "[::1]:0", "127.0.0.1:9002"} {
		c.ReturnRoutes[0].Destination = dest
		if err := c.Validate(); err == nil {
			t.Fatal("accepted unsafe scoped route", dest)
		}
	}
	c.Listeners[0].Address = "[::1]:9001"
	c.ReturnRoutes[0].Destination = "[::1]:9002"
	if err := c.Validate(); err != nil {
		t.Fatal("rejected IPv6 unicast", err)
	}
}
