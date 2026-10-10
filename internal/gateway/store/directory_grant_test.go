//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestGlobalDiscoveryGrantPermissionNilTargetAndRevocation(t *testing.T) {
	for _, operation := range []core.Operation{core.Directory, core.ChannelsRead, core.AgentsRead} {
		t.Run(string(operation), func(t *testing.T) { testGlobalDiscoveryGrant(t, operation) })
	}
}

func testGlobalDiscoveryGrant(t *testing.T, operation core.Operation) {
	c, stale, _ := fixture(t)
	c.Principals[0].Operations = []core.Operation{operation}
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
	r := core.Request{Version: 1, RequestID: "discovery", Operation: operation}
	if operation == core.Directory || operation == core.AgentsRead {
		r.Limit = 256
	}
	now := time.Now()
	if _, err := s.AuthorizeRead(t.Context(), stale, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("global discovery granted to old credential", err)
	}
	grant, err := s.AuthorizeRead(t.Context(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.AuthorizeRead(t.Context(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expired.Consume(now.Add(ReadLeaseDuration)); !errors.Is(err, ErrNotReady) {
		t.Fatal("expired global discovery grant consumed", err)
	}
	s.config.Principals[0].Operations = nil
	if _, err := s.AuthorizeRead(t.Context(), identity, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("global discovery bypassed current permission", err)
	}
	s.config.Principals[0].Operations = []core.Operation{operation}
	if _, err := s.Revoke(t.Context(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeRead(t.Context(), identity, r, now); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("global discovery bypassed durable revocation", err)
	}
	attempt, err := grant.Consume(now)
	if err != nil || attempt.Request.Target != nil || attempt.Request.Operation != operation || attempt.Deadline != now.Add(ReadLeaseDuration) {
		t.Fatal("pre-revocation global discovery lease changed", attempt, err)
	}
	if _, err := grant.Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("global discovery grant consumed twice", err)
	}
}
