package config

import "testing"

func TestPlainCoAPTCPConfig(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"valid":    func(*Config) {},
		"wildcard": func(c *Config) { c.Listeners[0].Address = "0.0.0.0:5683" },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile = "/private/cert"
			c.Listeners[0].TLSKeyFile = "/private/key"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := udpConfig()
			c.Listeners[0].Protocol = "coap_tcp"
			mutate(&c)
			if err := c.Validate(); (name != "tls") != (err == nil) {
				t.Fatal("CoAP over TCP validation", err)
			}
		})
	}
}
