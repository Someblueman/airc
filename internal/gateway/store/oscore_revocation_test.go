//go:build darwin || linux

package store

import (
	"errors"
	"testing"
)

func TestOSCOREProvisionRefusesDurableRevocationAcrossRestart(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	key := oscoreKey()
	if _, err := s.Revoke(t.Context(), key.Principal); err != nil {
		t.Fatal(err)
	}
	if err := s.ProvisionOSCORE(t.Context(), key); !errors.Is(err, ErrInvalid) {
		t.Fatal("durably revoked principal provisioned", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// The original config still says active; the journal's revocation wins.
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProvisionOSCORE(t.Context(), key); !errors.Is(err, ErrInvalid) {
		t.Fatal("restart cleared provisioning revocation", err)
	}
	if _, err := s.LoadOSCORE(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed provisioning created state", err)
	}
}
