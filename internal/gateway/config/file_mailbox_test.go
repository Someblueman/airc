package config

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestFileMailboxConfiguration(t *testing.T) {
	base := Defaults()
	base.Version, base.Realm, base.StateDir = 1, "fixture", "/tmp/state"
	base.Listeners = []Listener{{ID: "files", Protocol: "file_mailbox", Address: "/tmp/mailbox", Enabled: Available("unix"), Operations: []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}}}
	base.CursorKeyFile = "/tmp/cursor"
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"relative":     func(c *Config) { c.Listeners[0].Address = "mailbox" },
		"empty":        func(c *Config) { c.Listeners[0].Address = "" },
		"tls":          func(c *Config) { c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = "/tmp/cert", "/tmp/key" },
		"watch":        func(c *Config) { c.Listeners[0].Operations = []core.Operation{core.Watch} },
		"implicit_ops": func(c *Config) { c.Listeners[0].Operations = nil },
		"mixed": func(c *Config) {
			c.Listeners[0].Enabled = true
			c.Listeners = append(c.Listeners, Listener{ID: "http", Protocol: "http", Address: "127.0.0.1:0", Enabled: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Listeners = append([]Listener(nil), base.Listeners...)
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid file mailbox configuration accepted")
			}
		})
	}
}
