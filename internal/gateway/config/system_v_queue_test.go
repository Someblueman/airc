package config

import "testing"

func systemVQueueConfig(t *testing.T) Config {
	t.Helper()
	c := unixgramConfig(t)
	c.Listeners[0].ID = "queue"
	c.Listeners[0].Protocol = "system_v_queue"
	c.Listeners[0].Address = "2147483647"
	c.Listeners[0].Enabled = false
	c.ReturnRoutes[0].Protocol = "system_v_queue"
	c.ReturnRoutes[0].Listener = "queue"
	c.ReturnRoutes[0].Destination = "1"
	c.RecipientKeys = []RecipientKey{{ID: "ingress", PrivateKeyFile: "/private/ingress"}}
	return c
}

func TestSystemVQueueCanonicalKeys(t *testing.T) {
	for _, valid := range []string{"1", "1234", "2147483647"} {
		if _, err := SystemVQueueKey(valid); err != nil {
			t.Fatal(valid, err)
		}
	}
	for _, invalid := range []string{"", "0", "-1", "+1", "01", " 1", "1 ", "1\n", "0x10", "2147483648", "/queue", "127.0.0.1:1"} {
		if _, err := SystemVQueueKey(invalid); err == nil {
			t.Fatal("accepted", invalid)
		}
	}
}

func TestSystemVQueueProtectedConfiguration(t *testing.T) {
	c := systemVQueueConfig(t)
	if err := c.validateSystemVQueues(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.validateSystemVQueues(); (err == nil) != systemVQueueAvailable() {
		t.Fatal("platform availability", err)
	}
	// An unused validator must not advertise an enabled service implementation.
	if err := c.Validate(); err == nil {
		t.Fatal("activated unsupported service")
	}
}

func TestSystemVQueueConfigurationRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty ingress", func(c *Config) { c.Listeners[0].Address = "" }},
		{"zero response", func(c *Config) { c.ReturnRoutes[0].Destination = "0" }},
		{"ingress response", func(c *Config) { c.ReturnRoutes[0].Destination = c.Listeners[0].Address }},
		{"other ingress response", func(c *Config) {
			l := c.Listeners[0]
			l.ID = "other"
			l.Address = c.ReturnRoutes[0].Destination
			c.Listeners = append(c.Listeners, l)
		}},
		{"duplicate ingress", func(c *Config) { l := c.Listeners[0]; l.ID = "other"; c.Listeners = append(c.Listeners, l) }},
		{"wrong listener", func(c *Config) { c.ReturnRoutes[0].Listener = "other" }},
		{"missing recipient", func(c *Config) { c.ReturnRoutes[0].RecipientKeyID = "" }},
		{"relative key", func(c *Config) { c.ReturnRoutes[0].RecipientPublicKeyFile = "recipient.key" }},
		{"POSIX attributes", func(c *Config) { c.Listeners[0].POSIXQueue = &POSIXQueue{Depth: 1} }},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }},
		{"origins", func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} }},
		{"missing route", func(c *Config) { c.Listeners[0].Enabled = true; c.ReturnRoutes = nil }},
		{"ambiguous route", func(c *Config) {
			c.Listeners[0].Enabled = true
			c.ReturnRoutes = append(c.ReturnRoutes, c.ReturnRoutes[0])
		}},
		{"missing signing", func(c *Config) { c.Listeners[0].Enabled = true; c.ResponseSigningKey = nil }},
		{"missing ingress keys", func(c *Config) { c.Listeners[0].Enabled = true; c.RecipientKeys = nil }},
		{"missing principals", func(c *Config) { c.Listeners[0].Enabled = true; c.Principals = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := systemVQueueConfig(t)
			tc.mutate(&c)
			if err := c.validateSystemVQueues(); err == nil {
				t.Fatal("invalid queue configuration accepted")
			}
		})
	}
}
