package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func unixgramConfig(t *testing.T) Config {
	t.Helper()
	c := Defaults()
	c.Version, c.Realm, c.StateDir = 1, "realm", filepath.Join(t.TempDir(), "state")
	c.Bindings = []Binding{{ID: "binding", Network: "unix", Address: filepath.Join(t.TempDir(), "upstream.sock"), Account: "owner", AccountTokenFile: "/private/account"}}
	c.ResponseSigningKey = &ResponseSigningKey{ID: "response", PrivateKeyFile: "/private/response.key"}
	listenerPath := "/tmp/airc-gateway-unixgram.sock"
	mailboxPath := "/tmp/airc-gateway-unixgram-mailbox.sock"
	c.Listeners = []Listener{{ID: "unixgram", Protocol: "unixgram", Enabled: true, Address: listenerPath, Operations: []core.Operation{core.Send}}}
	c.Principals = []Principal{{ID: "principal", Binding: "binding", SigningKeys: []SigningKey{{ID: "sign", PublicKeyFile: "/public/sign"}}, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#room"}}}}
	c.ReturnRoutes = []ReturnRoute{{ID: "route", Principal: "principal", Protocol: "unixgram", Listener: "unixgram", Destination: mailboxPath, RecipientKeyID: "recipient", RecipientPublicKeyFile: "/public/recipient"}}
	return c
}

func TestUnixgramConfigValidatesFixedMailboxRoute(t *testing.T) {
	if !UnixAvailable() {
		t.Skip("unix sockets unavailable")
	}
	c := unixgramConfig(t)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixgramConfigRejectsStreamAndUDPRouteShapes(t *testing.T) {
	c := unixgramConfig(t)
	c.ReturnRoutes[0].Destination = "relative.sock"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted relative response mailbox")
	}
	c = unixgramConfig(t)
	c.ReturnRoutes[0].Protocol = "udp"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted UDP route on unixgram listener")
	}
	c = unixgramConfig(t)
	c.Listeners[0].TLSCertFile = "/private/cert"
	c.Listeners[0].TLSKeyFile = "/private/key"
	if err := c.Validate(); err == nil {
		t.Fatal("accepted TLS on unixgram listener")
	}
}

func TestUnixgramConfigAuthorModeAndOverlongPath(t *testing.T) {
	// A unixgram listener without routes takes plain author requests and
	// needs no principals, keys or response signing.
	c := unixgramConfig(t)
	c.ReturnRoutes, c.Principals, c.Bindings, c.ResponseSigningKey, c.RecipientKeys = nil, nil, nil, nil, nil
	if err := c.Validate(); err != nil || !c.LocalAuthorMode(c.Listeners[0]) {
		t.Fatal("route-less unixgram listener is not an author listener", err)
	}
	c = unixgramConfig(t)
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	if len(c.Listeners[0].Address) < limit+1 {
		c.Listeners[0].Address = "/" + strings.Repeat("x", limit)
	}
	if err := c.Validate(); err == nil {
		t.Fatal("accepted overlong unixgram listener path")
	}
}
