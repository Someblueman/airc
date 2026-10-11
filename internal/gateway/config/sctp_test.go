package config

import (
	"testing"
)

func sctpConfig(protocol string) Config {
	c := udpConfig()
	c.Listeners[0].Protocol = protocol
	c.Listeners[0].Address = "192.0.2.10:0"
	return c
}

func TestSCTPConfigPlatformGate(t *testing.T) {
	for _, protocol := range []string{"sctp", "sctp_unordered", "sctp_pr_ttl"} {
		c := sctpConfig(protocol)
		c.Listeners[0].Enabled = false
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: valid disabled config rejected: %v", protocol, err)
		}
		c.Listeners[0].Enabled = true
		err := c.Validate()
		if Available("sctp") && err != nil {
			t.Fatalf("%s: enabled config rejected on Linux ARM64: %v", protocol, err)
		}
		if !Available("sctp") && err == nil {
			t.Fatalf("%s: enabled config accepted on an unqualified target", protocol)
		}
	}
}

func TestSCTPConfigAcceptsLiteralIPv4AndIPv6(t *testing.T) {
	for _, address := range []string{"192.0.2.10:0", "[2001:db8::10]:65535"} {
		t.Run(address, func(t *testing.T) {
			c := sctpConfig("sctp")
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

func TestSCTPConfigRejectsInvalidAddresses(t *testing.T) {
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
		"origins": func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.test"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := sctpConfig("sctp")
			c.Listeners[0].Enabled = true
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid SCTP configuration accepted")
			}
		})
	}
}
