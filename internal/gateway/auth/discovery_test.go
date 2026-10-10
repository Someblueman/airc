//go:build unix

package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSignedDiscoveryWithoutOperationPermissions(t *testing.T) {
	c, key, _ := messageFixture(t)
	c.Principals[0].Operations = nil
	c.Principals[0].Targets = nil
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	e := intent()
	e.Request = core.Request{Version: 1, RequestID: "discover", Operation: core.CapabilitiesGet}
	if _, err := v.AuthenticateSigned(signIntent(t, key, e), time.Unix(1000, 0)); err != nil {
		t.Fatal("authenticated principal cannot discover empty permissions", err)
	}
	if err := (Identity{}).Authorize(e.Request); !errors.Is(err, ErrForbidden) {
		t.Fatal("discovery accepted absent identity", err)
	}
	c.Principals[0].Revoked = true
	v, err = New(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.AuthenticateSigned(signIntent(t, key, e), time.Unix(1000, 0)); !errors.Is(err, ErrAuthentication) {
		t.Fatal("discovery accepted revoked signer", err)
	}
}
