//go:build unix

package auth

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestIRCGrantRequiresActualVerifierAuthority(t *testing.T) {
	server, b, _ := ircSessionFixture(t, "valid")
	identity := server.grants[[2]string{"alice", "#external"}].identity
	c, _ := fixtureConfig(t)
	c.Principals[0].Operations = []core.Operation{core.Send}
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	// Configuration claims cannot expand the supplied verifier's real grants.
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#other"}}
	b.Routes[0].Target = c.Principals[0].Targets[0]
	if _, err := NewIRCServer(v, c, b); err != ErrCredential {
		t.Fatal("configuration laundered target", err)
	}
	b.Routes[0].Target = identity.Targets()[0]
	c.Principals[0].Targets = identity.Targets()
	v.principals[0].revoked = true
	if _, err := NewIRCServer(v, c, b); err != ErrCredential {
		t.Fatal("revoked principal granted", err)
	}
	if _, err := NewIRCServer(nil, c, b); err != ErrCredential {
		t.Fatal("nil verifier granted", err)
	}
}
