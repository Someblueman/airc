package config

import "testing"

func TestCoAPUDPConfig(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"valid":     func(*Config) {},
		"multicast": func(c *Config) { c.Listeners[0].Address = "239.0.0.1:5683" },
		"tls": func(c *Config) {
			c.Listeners[0].TLSCertFile = "/private/cert"
			c.Listeners[0].TLSKeyFile = "/private/key"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := udpConfig()
			c.Listeners[0].Protocol = "coap_udp"
			mutate(&c)
			err := c.Validate()
			if (name == "valid") != (err == nil) {
				t.Fatal("CoAP UDP validation", err)
			}
		})
	}
}
