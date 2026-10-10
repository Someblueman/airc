//go:build unix

package auth

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAnonymousIdentityIsListenerScopedAndRevocable(t *testing.T) {
	c, _, _ := messageFixture(t)
	c.Principals[0].BearerTokenFile, c.Principals[0].SigningKeys = "", nil
	c.Listeners = []config.Listener{{ID: "guest", Protocol: "udp", Enabled: true, Address: "127.0.0.1:0", AnonymousPrincipal: c.Principals[0].ID, Operations: []core.Operation{core.Send}}}
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	i, err := v.Anonymous("guest")
	if err != nil || i.ID() != c.Principals[0].ID || !v.OwnsIdentity(i) {
		t.Fatalf("guest identity: %v", err)
	}
	if _, err := v.Authenticate(""); err == nil {
		t.Fatal("anonymous listener disabled bearer authentication globally")
	}
	if _, err := v.Anonymous("different-listener"); err == nil {
		t.Fatal("anonymous identity escaped its listener")
	}
	for _, disabled := range []bool{false, true} {
		c.Listeners[0].Enabled = !disabled
		c.Principals[0].Revoked = !disabled
		v, err := New(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Anonymous("guest"); err == nil {
			t.Fatal("revoked identity or disabled listener allowed anonymous access")
		}
	}
}
