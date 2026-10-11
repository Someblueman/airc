package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func syslogConfig() Config {
	c := mqttConfig()
	c.Listeners = []Listener{{ID: "syslog", Protocol: "syslog_tls", TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/tls-key", Operations: []core.Operation{core.Send}, Syslog: &Syslog{Routes: []SyslogRoute{{CertificateSHA256: strings.Repeat("a", 64), BoundAuthor: BoundAuthor{Nick: "syslog-host", AccountTokenFile: "/tmp/syslog.token"}, Target: fixtureRoom}}}}}
	return c
}
func TestSyslogConfiguration(t *testing.T) {
	c := syslogConfig()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(b)
	if err != nil || parsed.Listeners[0].Syslog.Routes[0].BoundAuthor.Nick != "syslog-host" {
		t.Fatal("published grant parse", err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled wired profile", err)
	}
	for name, mutate := range map[string]func(*Config){
		"missing":  func(c *Config) { c.Listeners[0].Syslog = nil },
		"protocol": func(c *Config) { c.Listeners[0].Protocol = "http" },
		"TLS":      func(c *Config) { c.Listeners[0].TLSCertFile = ""; c.Listeners[0].TLSKeyFile = "" },
		"empty":    func(c *Config) { c.Listeners[0].Syslog.Routes = nil },
		"too_many": func(c *Config) { c.Listeners[0].Syslog.Routes = make([]SyslogRoute, 129) },
		"duplicate": func(c *Config) {
			c.Listeners[0].Syslog.Routes = append(c.Listeners[0].Syslog.Routes, c.Listeners[0].Syslog.Routes[0])
		},
		"pin_length":       func(c *Config) { c.Listeners[0].Syslog.Routes[0].CertificateSHA256 = "aa" },
		"pin_hex":          func(c *Config) { c.Listeners[0].Syslog.Routes[0].CertificateSHA256 = strings.Repeat("z", 64) },
		"pin_case":         func(c *Config) { c.Listeners[0].Syslog.Routes[0].CertificateSHA256 = strings.Repeat("A", 64) },
		"operation":        func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.HistoryRead} },
		"no_author":        func(c *Config) { c.Listeners[0].Syslog.Routes[0].BoundAuthor = BoundAuthor{} },
		"canonical_target": func(c *Config) { c.Listeners[0].Syslog.Routes[0].Target.Name = " fixture " },
		"origin":           func(c *Config) { c.Listeners[0].AllowedOrigins = []string{"https://example.com"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := syslogConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe grant accepted")
			}
		})
	}
}
