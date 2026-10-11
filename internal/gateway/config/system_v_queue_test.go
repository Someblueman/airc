package config

import "testing"

func systemVQueueConfig(t *testing.T) Config {
	t.Helper()
	c := unixgramConfig(t)
	c.Listeners[0].ID = "queue"
	c.Listeners[0].Protocol = "system_v_queue"
	c.Listeners[0].Address = "2147483647"
	c.Listeners[0].Enabled = false
	c.Listeners[0].ReplyTo = "1"
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

func TestSystemVQueueConfiguration(t *testing.T) {
	c := systemVQueueConfig(t)
	if err := c.validateSystemVQueues(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Enabled = true
	if err := c.validateSystemVQueues(); (err == nil) != systemVQueueAvailable() {
		t.Fatal("platform availability", err)
	}
	if err := c.Validate(); (err == nil) != systemVQueueAvailable() {
		t.Fatal("public platform availability", err)
	}
}

func TestSystemVQueueConfigurationRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty ingress", func(c *Config) { c.Listeners[0].Address = "" }},
		{"zero reply", func(c *Config) { c.Listeners[0].ReplyTo = "0" }},
		{"ingress reply", func(c *Config) { c.Listeners[0].ReplyTo = c.Listeners[0].Address }},
		{"duplicate ingress", func(c *Config) { l := c.Listeners[0]; l.ID = "other"; c.Listeners = append(c.Listeners, l) }},
		{"missing reply", func(c *Config) { c.Listeners[0].Enabled = true; c.Listeners[0].ReplyTo = "" }},
		{"POSIX attributes", func(c *Config) { c.Listeners[0].POSIXQueue = &POSIXQueue{Depth: 1} }},
		{"TLS", func(c *Config) { c.Listeners[0].TLSCertFile = "/cert"; c.Listeners[0].TLSKeyFile = "/key" }},
		{"origins", func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := systemVQueueConfig(t)
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("public validation accepted invalid queue configuration")
			}
		})
	}
}
