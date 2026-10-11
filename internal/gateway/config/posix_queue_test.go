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
	c.ReturnRoutes[0].Protocol = "posix_mqueue"
	c.ReturnRoutes[0].Listener = "queue"
	c.ReturnRoutes[0].Destination = "/agw-response"
	return c
}
func TestPOSIXQueueConfigurationShape(t *testing.T) {
	c := posixQueueConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
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
		{"relative response", func(c *Config) { c.ReturnRoutes[0].Destination = "queue" }},
		{"ingress response", func(c *Config) { c.ReturnRoutes[0].Destination = c.Listeners[0].Address }},
		{"wrong listener", func(c *Config) { c.ReturnRoutes[0].Listener = "other" }},
		{"missing recipient", func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" }},
		{"relative public key", func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "recipient.key" }},
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

func TestPOSIXQueueEnabledRouteScope(t *testing.T) {
	c := posixQueueConfig(t)
	c.Listeners[0].Enabled = true
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
	if err := c.validatePOSIXQueues(); POSIXQueueAvailable() && err != nil {
		t.Fatal(err)
	}
	// Without return routes the queue takes plain author requests.
	author := c
	author.ReturnRoutes = nil
	if err := author.validatePOSIXQueues(); err != nil || !author.LocalAuthorMode(author.Listeners[0]) {
		t.Fatal("route-less POSIX queue is not an author listener", err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.ReturnRoutes = append(c.ReturnRoutes, c.ReturnRoutes[0]) },
		func(c *Config) { c.ResponseSigningKey = nil },
		func(c *Config) { c.RecipientKeys = nil },
	} {
		bad := c
		bad.ReturnRoutes = append([]ReturnRoute(nil), c.ReturnRoutes...)
		mutate(&bad)
		if err := bad.validatePOSIXQueues(); err == nil {
			t.Fatal("missing protection or ambiguous route accepted")
		}
	}
}
