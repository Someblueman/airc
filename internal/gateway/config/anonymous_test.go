package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func anonymousConfig() Config {
	c := udpConfig(1)
	c.Listeners[0].AnonymousPrincipal = c.Principals[0].ID
	c.Principals[0].SigningKeys = nil
	c.ResponseSigningKey, c.RecipientKeys, c.ReturnRoutes = nil, nil, nil
	return c
}

func TestAnonymousUDPNeedsExplicitListenerAndServerIdentity(t *testing.T) {
	c := anonymousConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Config){
		"no_opt_in":             func(c *Config) { c.Listeners[0].AnonymousPrincipal = "" },
		"unknown_identity":      func(c *Config) { c.Listeners[0].AnonymousPrincipal = "missing" },
		"other_protocol":        func(c *Config) { c.Listeners[0].Protocol = "coap_udp" },
		"unsupported_operation": func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.ProfileUpdate} },
		"fixed_reply_route":     func(c *Config) { c.ReturnRoutes = udpConfig(1).ReturnRoutes },
	} {
		t.Run(name, func(t *testing.T) {
			c := anonymousConfig()
			change(&c)
			if c.Validate() == nil {
				t.Fatal("invalid anonymous configuration accepted")
			}
		})
	}
}
