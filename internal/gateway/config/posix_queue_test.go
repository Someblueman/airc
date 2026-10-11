package config

import (
	"strings"
	"testing"
)

func posixQueueConfig(t *testing.T) Config {
	c := unixgramConfig(t)
	c.Listeners[0].ID = "queue"
	c.Listeners[0].Protocol = "posix_mqueue"
	c.Listeners[0].Address = "/agw-ingress"
	c.Listeners[0].POSIXQueue = &POSIXQueue{Depth: 8}
	c.Listeners[0].Enabled = false
	c.Listeners[0].ReplyTo = "/agw-response"
	return c
}
func TestPOSIXQueueConfigurationShape(t *testing.T) {
	c := posixQueueConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); (err == nil) != POSIXQueueAvailable() {
		t.Fatalf("enabled platform availability: %v", err)
	}
}
func TestPOSIXQueueConfigurationRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing attributes", func(c *Config) { c.Listeners[0].POSIXQueue = nil }},
		{"zero depth", func(c *Config) { c.Listeners[0].POSIXQueue.Depth = 0 }},
		{"excess depth", func(c *Config) { c.Listeners[0].POSIXQueue.Depth = 11 }},
		{"traversal", func(c *Config) { c.Listeners[0].Address = "/../queue" }},
		{"overlong", func(c *Config) { c.Listeners[0].Address = "/" + strings.Repeat("x", 255) }},
		{"relative reply", func(c *Config) { c.Listeners[0].ReplyTo = "queue" }},
		{"ingress reply", func(c *Config) { c.Listeners[0].ReplyTo = c.Listeners[0].Address }},
		{"missing reply", func(c *Config) { c.Listeners[0].Enabled = true; c.Listeners[0].ReplyTo = "" }},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }},
		{"other protocol", func(c *Config) { c.Listeners[0].Protocol = "unixgram" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := posixQueueConfig(t)
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid queue configuration accepted")
			}
		})
	}
}
