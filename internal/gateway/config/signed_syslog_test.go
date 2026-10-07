package config

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testsyslog"
)

func TestSignedSyslogConfiguration(t *testing.T) {
	now := time.Now()
	origin := testsyslog.New(t, now.Add(-time.Hour), now.Add(time.Hour))
	makeConfig := func() Config {
		c := syslogConfig()
		c.Listeners[0].Protocol = "syslog_signed"
		c.Listeners[0].SignedSyslog = &SignedSyslog{Routes: []SignedSyslogRoute{{CertificateDER: base64.StdEncoding.EncodeToString(origin.DER), Hostname: "signed.example", Principal: "producer", Target: c.Principals[0].Targets[0]}}}
		return c
	}
	c := makeConfig()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err != nil {
		t.Fatal("disabled origin grant", err)
	}
	c.Listeners[0].Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatal("enabled signed listener", err)
	}
	for name, mutate := range map[string]func(*Config){
		"wrong_protocol":    func(c *Config) { c.Listeners[0].Protocol = "syslog_tls" },
		"missing_origins":   func(c *Config) { c.Listeners[0].SignedSyslog = nil },
		"missing_transport": func(c *Config) { c.Listeners[0].Syslog = nil },
		"duplicate_hostname": func(c *Config) {
			r := c.Listeners[0].SignedSyslog.Routes[0]
			r.Hostname = strings.ToUpper(r.Hostname)
			c.Listeners[0].SignedSyslog.Routes = append(c.Listeners[0].SignedSyslog.Routes, r)
		},
		"wildcard":              func(c *Config) { c.Listeners[0].SignedSyslog.Routes[0].Hostname = "*.example" },
		"invalid_certificate":   func(c *Config) { c.Listeners[0].SignedSyslog.Routes[0].CertificateDER = "AAAA" },
		"oversized_certificate": func(c *Config) { c.Listeners[0].SignedSyslog.Routes[0].CertificateDER = strings.Repeat("A", 12001) },
		"wrong_target":          func(c *Config) { c.Listeners[0].SignedSyslog.Routes[0].Target.Name = "#other" },
		"unknown_principal":     func(c *Config) { c.Listeners[0].SignedSyslog.Routes[0].Principal = "unknown" },
		"revoked":               func(c *Config) { c.Principals[0].Revoked = true },
		"read_only":             func(c *Config) { c.Principals[0].Operations = []core.Operation{core.HistoryRead} },
	} {
		t.Run(name, func(t *testing.T) {
			c := makeConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("unsafe origin grant accepted")
			}
		})
	}
	for _, name := range []string{"signed.example", "SIGNED.EXAMPLE", "127.0.0.1", "::1"} {
		if !SignedSyslogHostname(name) {
			t.Fatal("literal hostname refused", name)
		}
	}
	for _, name := range []string{"-", "localhost", "*.example", "x..example", "-x.example", "x.example-", "雪.example", "fe80::1%en0"} {
		if SignedSyslogHostname(name) {
			t.Fatal("unsafe hostname accepted", name)
		}
	}
}
