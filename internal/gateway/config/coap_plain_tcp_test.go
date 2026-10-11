package config

import "testing"

func TestPlainCoAPTCPRequiresProtectedConnectionRoute(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"valid":               func(*Config) {},
		"missing_signer":      func(c *Config) { c.ResponseSigningKey = nil },
		"wrong_listener":      func(c *Config) { c.ReturnRoutes[0].Listener = "other" },
		"wrong_protocol":      func(c *Config) { c.ReturnRoutes[0].Protocol = "coap_udp" },
		"network_destination": func(c *Config) { c.ReturnRoutes[0].Destination = "127.0.0.1:1234" },
		"missing_recipient":   func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "" },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile = "/private/cert"
			c.Listeners[0].TLSKeyFile = "/private/key"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := udpConfig(1)
			c.Listeners[0].Protocol = "coap_tcp"
			c.ReturnRoutes[0].Protocol = "coap_tcp"
			c.ReturnRoutes[0].Destination = "connection"
			mutate(&c)
			if err := c.Validate(); (name == "valid") != (err == nil) {
				t.Fatal("CoAP connection route", err)
			}
		})
	}
}
