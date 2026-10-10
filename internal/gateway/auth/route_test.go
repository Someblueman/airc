package auth

import (
	"errors"
	"testing"
)

func TestRouteResolvesOnlyConfiguredActivePrincipals(t *testing.T) {
	v := &Verifier{principals: []principal{{id: "author", binding: "author"}, {id: "revoked", revoked: true}}}
	identity, err := v.Route("author")
	if err != nil || identity.ID() != "author" || !v.OwnsIdentity(identity) {
		t.Fatalf("route identity=%q err=%v", identity.ID(), err)
	}
	for _, name := range []string{"", "revoked", "unknown"} {
		if _, err := v.Route(name); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("principal %q: %v", name, err)
		}
	}
	if _, err := (*Verifier)(nil).Route("author"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("nil verifier: %v", err)
	}
}
