//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestPermissionsSnapshotsAndDurableRevocation(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	a, err := s.Permissions(ctx, identity)
	if err != nil || !slices.Equal(a.Operations, c.Principals[0].Operations) || !slices.Equal(a.Targets, c.Principals[0].Targets) || a.Retry != c.Retry {
		t.Fatal("permission snapshot mismatch", a, err)
	}
	a.Operations[0] = core.Directory
	a.Targets[0].Name = "#changed"
	targets := identity.Targets()
	targets[0].Name = "#identity-changed"
	a, err = s.Permissions(ctx, identity)
	if err != nil || !slices.Equal(a.Operations, c.Principals[0].Operations) || !slices.Equal(a.Targets, c.Principals[0].Targets) {
		t.Fatal("returned permissions modified authority", a, err)
	}
	if _, err := s.Permissions(ctx, auth.Identity{}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("empty identity accepted", err)
	}
	if _, err := s.Revoke(ctx, identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Permissions(ctx, identity); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("stale authenticated snapshot bypassed revocation", err)
	}
}
