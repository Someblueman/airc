//go:build darwin || linux

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRetainedSendLookupPreservesJournalAndAuthority(t *testing.T) {
	c, identity, other := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r := request()
	issued, expires := int64(1000), int64(1100)
	r.IssuedAt, r.ExpiresAt = &issued, &expires
	before, _, err := s.Admit(t.Context(), identity, r, 1160, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	proof := auth.RetainedSend{Identity: identity, Request: r}
	for i := range 2 {
		if i == 1 {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.LookupRetainedSend(t.Context(), proof)
		if err != nil || !reflect.DeepEqual(got, before) {
			t.Fatalf("retained job changed: %+v %v", got, err)
		}
	}
	changed := proof
	changed.Request.Message = "different"
	if _, err := s.LookupRetainedSend(t.Context(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed intent accepted", err)
	}
	changed = proof
	changed.Request.RequestID = "unknown"
	if _, err := s.LookupRetainedSend(t.Context(), changed); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing job admitted", err)
	}
	changed = proof
	changed.Identity = other
	if _, err := s.LookupRetainedSend(t.Context(), changed); !errors.Is(err, ErrNotFound) {
		t.Fatal("other identity acquired job", err)
	}
	changed = proof
	changed.Request.Operation = core.HistoryRead
	if _, err := s.LookupRetainedSend(t.Context(), changed); !errors.Is(err, ErrInvalid) {
		t.Fatal("read proof accepted", err)
	}
	if _, err := s.Revoke(t.Context(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupRetainedSend(t.Context(), proof); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("durable revocation bypassed", err)
	}
}
