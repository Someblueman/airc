package config

import (
	"runtime"
	"testing"
)

func icmpV6Config(enabled bool) Config {
	c := udpConfig()
	c.Listeners = []Listener{{
		ID: "icmp", Protocol: "icmp_v6", Enabled: enabled, Address: "[::1]:9001",
		ICMPv6: &ICMPv6{Interface: "lo0"}, Operations: c.Listeners[0].Operations,
	}}
	return c
}

func TestICMPv6PlatformGate(t *testing.T) {
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

func TestICMPv6RejectsMalformedProfiles(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"missing endpoint":          func(c *Config) { c.Listeners[0].Address = "" },
		"missing interface":         func(c *Config) { c.Listeners[0].ICMPv6.Interface = "" },
		"TLS":                       func(c *Config) { c.Listeners[0].TLSCertFile = "/private/cert" },
		"origins":                   func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
		"options on other protocol": func(c *Config) { c.Listeners[0].Protocol = "udp" },
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
