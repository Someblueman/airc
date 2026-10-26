package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func unixgramConfig(t *testing.T) Config {
	t.Helper()
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "realm", filepath.Join(t.TempDir(), "state")
	c.Upstream = &Upstream{Network: "unix", Address: filepath.Join(t.TempDir(), "upstream.sock")}
	c.Listeners = []Listener{{ID: "unixgram", Protocol: "unixgram", Enabled: true, Address: "/tmp/airc-gateway-unixgram.sock", Operations: []core.Operation{core.Send}}}
	return c
}

// A unixgram listener takes plain author requests and replies to each
// sender's bound address: it needs no keys or mailboxes.
func TestUnixgramConfig(t *testing.T) {
	if !Available("unix") {
		t.Skip("unix sockets unavailable")
	}
	c := unixgramConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listeners[0].Address = "relative.sock"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted relative socket path")
	}
	c = unixgramConfig(t)
	c.Listeners[0].TLSCertFile = "/private/cert"
	c.Listeners[0].TLSKeyFile = "/private/key"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted TLS on unixgram listener")
	}
	c = unixgramConfig(t)
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	c.Listeners[0].Address = "/" + strings.Repeat("x", limit)
	if err := c.Validate(); err == nil {
		t.Fatal("accepted overlong unixgram listener path")
	}
}
