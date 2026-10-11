package config

import "testing"

func TestCoAPUDPConfigRequiresProtectedMatchingMailbox(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"valid":             func(*Config) {},
		"missing_signer":    func(c *Config) { c.ResponseSigningKey = nil },
		"wrong_protocol":    func(c *Config) { c.ReturnRoutes[0].Protocol = "udp" },
		"missing_recipient": func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "" },
		"multicast":         func(c *Config) { c.ReturnRoutes[0].Destination = "239.0.0.1:1234" },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile = "/private/cert"
			c.Listeners[0].TLSKeyFile = "/private/key"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := udpConfig(1)
			c.Listeners[0].Protocol = "coap_udp"
			c.ReturnRoutes[0].Protocol = "coap_udp"
			mutate(&c)
			err := c.Validate()
			if (name == "valid") != (err == nil) {
				t.Fatal("CoAP route validation", err)
			}
		})
	}
}
