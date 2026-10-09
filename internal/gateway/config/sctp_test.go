package config

import (
	"testing"
)

func sctpConfig() Config {
	c := udpConfig(1)
	c.Listeners[0].Protocol = "sctp"
	c.Listeners[0].Address = "192.0.2.10:0"
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress.key"}}
	c.ReturnRoutes[0].Protocol = "sctp"
	c.ReturnRoutes[0].Destination = "connection"
	return c
}

func TestSCTPConfigRequiresEncryptedConnectionRoutes(t *testing.T) {
	c := sctpConfig()
	c.Listeners[0].Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatalf("valid disabled SCTP config rejected: %v", err)
	}
	c.Listeners[0].Enabled = true
	err := c.Validate()
	if SCTPAvailable() {
		if err != nil {
			t.Fatal(err)
		}
	} else if err == nil {
		t.Fatal("enabled SCTP accepted on an unqualified target")
	}
}

func TestSCTPConfigAcceptsLiteralIPv4AndIPv6(t *testing.T) {
	for _, address := range []string{"192.0.2.10:0", "[2001:db8::10]:65535"} {
		t.Run(address, func(t *testing.T) {
			c := sctpConfig()
			c.Listeners[0].Address = address
			if err := sctpListenEndpoint(address); err != nil {
				t.Fatal(err)
			}
			c.Listeners[0].Enabled = false
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSCTPConfigRejectsInvalidAddressesAndProtection(t *testing.T) {
	cases := map[string]func(*Config){
		"empty address":       func(c *Config) { c.Listeners[0].Address = "" },
		"DNS host":            func(c *Config) { c.Listeners[0].Address = "sctp.example:1234" },
		"unspecified address": func(c *Config) { c.Listeners[0].Address = "0.0.0.0:1234" },
		"multicast address":   func(c *Config) { c.Listeners[0].Address = "224.0.0.1:1234" },
		"broadcast address":   func(c *Config) { c.Listeners[0].Address = "255.255.255.255:1234" },
		"IPv4-mapped IPv6":    func(c *Config) { c.Listeners[0].Address = "[::ffff:127.0.0.1]:1234" },
		"IPv6 zone":           func(c *Config) { c.Listeners[0].Address = "[fe80::1%lo0]:1234" },
		"invalid port":        func(c *Config) { c.Listeners[0].Address = "192.0.2.10:65536" },
		"TLS": func(c *Config) {
			c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/cert", "/key"
		},
		"origins":               func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.test"} },
		"anonymous":             func(c *Config) { c.Listeners[0].AnonymousPrincipal = "a" },
		"missing signer":        func(c *Config) { c.ResponseSigningKey = nil },
		"missing ingress key":   func(c *Config) { c.RecipientKeys = nil },
		"missing recipient key": func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" },
		"wrong destination":     func(c *Config) { c.ReturnRoutes[0].Destination = "127.0.0.1:3000" },
		"missing route":         func(c *Config) { c.ReturnRoutes = nil },
		"duplicate routes": func(c *Config) {
			duplicate := c.ReturnRoutes[0]
			duplicate.ID = "another"
			c.ReturnRoutes = append(c.ReturnRoutes, duplicate)
		},
		"missing principal signer": func(c *Config) { c.Principals[0].SigningKeys = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := sctpConfig()
			c.Listeners[0].Enabled = true
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid SCTP configuration accepted")
			}
		})
	}
}
