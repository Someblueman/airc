//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestDirectoryGrantGlobalPermissionNilTargetAndRevocation(t *testing.T) {
	c, stale, _ := fixture(t)
	c.Principals[0].Operations = []core.Operation{core.Directory}
	c.Principals[0].Targets = nil
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "directory", Operation: core.Directory, Limit: 256}
	now := time.Now()
	if _, err := s.AuthorizeRead(t.Context(), stale, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("directory granted to old credential", err)
	}
	grant, err := s.AuthorizeRead(t.Context(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(t.Context(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeRead(t.Context(), identity, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("directory bypassed durable revocation", err)
	}
	attempt, err := grant.Consume(now)
	if err != nil || attempt.Request.Target != nil || attempt.Request.Operation != core.Directory || attempt.Deadline != now.Add(ReadLeaseDuration) {
		t.Fatal("pre-revocation directory lease changed", attempt, err)
	}
	if _, err := grant.Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("directory grant consumed twice", err)
	}
}
