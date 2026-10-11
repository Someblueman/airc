//go:build darwin || linux

package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func fixture(t *testing.T) (config.Config, auth.Identity, auth.Identity) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version = 1
	c.Realm = "fixture"
	c.StateDir = filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	secret := func(name string) string {
		p := filepath.Join(root, name)
		if err := auth.CreateCredential(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "irc.sock"), Account: "author", AccountTokenFile: secret("account")}}
	for _, name := range []string{"device", "other"} {
		c.Principals = append(c.Principals, config.Principal{ID: name, Binding: "backend", BearerTokenFile: secret(name), Operations: []core.Operation{core.Send, core.Reply, core.ReceiptGet}, Targets: []core.Target{{Kind: "room", Name: "#Allowed"}}})
	}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	identities := make([]auth.Identity, 2)
	for i, p := range c.Principals {
		token, err := auth.ReadToken(p.BearerTokenFile)
		if err != nil {
			t.Fatal(err)
		}
		identities[i], err = v.Authenticate(token)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c, identities[0], identities[1]
}

func request() core.Request {
	return core.Request{Version: 1, RequestID: "request", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#Allowed"}, Message: "first\r\nsecond"}
}

// shimScope is the author scope the principal shim gives fixture principals.
func shimScope(c config.Config) string {
	return core.Author{Nick: c.Bindings[0].Account, Registered: true}.Scope()
}

// scopeOf is the author scope a principal's jobs are keyed by.
func scopeOf(s *Store, identity auth.Identity) string {
	return s.principalScope(identity.ID(), identity.Binding())
}
