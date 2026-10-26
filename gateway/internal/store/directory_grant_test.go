//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestGlobalDiscoveryGrantPermissionNilTarget(t *testing.T) {
	for _, operation := range []core.Operation{core.Directory, core.ChannelsRead, core.AgentsRead} {
		t.Run(string(operation), func(t *testing.T) { testGlobalDiscoveryGrant(t, operation) })
	}
}

func testGlobalDiscoveryGrant(t *testing.T, operation core.Operation) {
	c, _, _ := fixture(t)
	identity := fixtureAuthor()
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
	grant, err := s.AuthorizeAuthorRead(t.Context(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := s.AuthorizeAuthorRead(t.Context(), identity, r, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expired.Consume(now.Add(ReadLeaseDuration)); !errors.Is(err, ErrNotReady) {
		t.Fatal("expired global discovery grant consumed", err)
	}
	attempt, err := grant.Consume(now)
	if err != nil || attempt.Request.Target != nil || attempt.Request.Operation != operation || attempt.Deadline != now.Add(ReadLeaseDuration) {
		t.Fatal("global discovery lease changed", attempt, err)
	}
	if _, err := grant.Consume(now); !errors.Is(err, ErrNotReady) {
		t.Fatal("global discovery grant consumed twice", err)
	}
}
