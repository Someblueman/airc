package config

import (
	"runtime"
	"testing"
)

func icmpV6Config(enabled bool) Config {
	c := udpConfig(1)
	c.Listeners = []Listener{{
		ID: "icmp", Protocol: "icmp_v6", Enabled: enabled, Address: "[::1]:9001",
		ICMPv6: &ICMPv6{Interface: "lo0"}, Operations: c.Listeners[0].Operations,
	}}
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress.key"}}
	c.ReturnRoutes = []ReturnRoute{{
		ID: "icmp-return", Principal: "a", Protocol: "icmp_v6", Listener: "icmp",
		Destination: "[::1]:9002", RecipientKeyID: "reply", RecipientPublicKeyFile: "/public/reply",
	}}
	return c
}

func TestICMPv6ProtectedRouteAndPlatformGate(t *testing.T) {
	c := icmpV6Config(false)
	if err := c.Validate(); err != nil {
		t.Fatalf("valid disabled ICMPv6 config rejected: %v", err)
	}
	c.Listeners[0].Enabled = true
	err := c.Validate()
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		if err != nil {
			t.Fatalf("enabled ICMPv6 config rejected on qualified platform: %v", err)
		}
	} else if err == nil {
		t.Fatal("enabled ICMPv6 config accepted outside Darwin ARM64")
	}
}

func TestICMPv6RejectsMalformedDisabledProfilesAndRoutes(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"missing endpoint":                  func(c *Config) { c.Listeners[0].Address = "" },
		"missing interface":                 func(c *Config) { c.Listeners[0].ICMPv6.Interface = "" },
		"wrong listener":                    func(c *Config) { c.ReturnRoutes[0].Listener = "other" },
		"wrong protocol":                    func(c *Config) { c.ReturnRoutes[0].Protocol = "udp" },
		"relative key":                      func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "reply" },
		"missing recipient":                 func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" },
		"TLS":                               func(c *Config) { c.Listeners[0].TLSCertFile = "/private/cert" },
		"origins":                           func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"anonymous":                         func(c *Config) { c.Listeners[0].AnonymousPrincipal = "a" },
		"options on other protocol":         func(c *Config) { c.Listeners[0].Protocol = "udp" },
		"same listener address and channel": func(c *Config) { c.ReturnRoutes[0].Destination = c.Listeners[0].Address },
		"unknown route principal":           func(c *Config) { c.ReturnRoutes[0].Principal = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			c := icmpV6Config(false)
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("accepted invalid ICMPv6 config")
			}
		})
	}
}

func TestEnabledICMPv6RequiresProtectedIngressAndRoutes(t *testing.T) {
	if !ICMPv6Available() {
		t.Skip("enabled ICMPv6 config is only qualified on Darwin ARM64")
	}
	for name, mutate := range map[string]func(*Config){
		"missing route": func(c *Config) { c.ReturnRoutes = nil },
		"duplicate route": func(c *Config) {
			duplicate := c.ReturnRoutes[0]
			duplicate.ID = "duplicate"
			c.ReturnRoutes = append(c.ReturnRoutes, duplicate)
		},
		"missing signer":      func(c *Config) { c.ResponseSigningKey = nil },
		"missing ingress key": func(c *Config) { c.RecipientKeys = nil },
		"missing principal":   func(c *Config) { c.Principals = nil },
		"bearer only":         func(c *Config) { c.Principals[0].SigningKeys = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c := icmpV6Config(true)
			mutate(&c)
			if err := c.validateICMPv6(); err == nil {
				t.Fatal("accepted incomplete enabled ICMPv6 config")
			}
		})
	}
}

func TestICMPv6RejectsDuplicateInterfaceEndpoint(t *testing.T) {
	c := icmpV6Config(false)
	duplicate := c.Listeners[0]
	duplicate.ID = "icmp-copy"
	duplicate.Enabled = true
	c.Listeners[0].Enabled = true
	c.Listeners = append(c.Listeners, duplicate)
	if err := c.validateICMPv6(); err == nil {
		t.Fatal("accepted duplicate enabled interface and endpoint")
	}
}

func TestICMPv6RouteMustUseCanonicalEndpoint(t *testing.T) {
	for _, endpoint := range []string{"[::1]:09002", "[::1]:0", "[::]:9002", "[ff02::1]:9002", "[fe80::1]:9002", "[::1%lo0]:9002", "127.0.0.1:9002"} {
		c := icmpV6Config(false)
		c.ReturnRoutes[0].Destination = endpoint
		if err := c.Validate(); err == nil {
			t.Errorf("accepted unsafe ICMPv6 route endpoint %q", endpoint)
		}
	}
}
