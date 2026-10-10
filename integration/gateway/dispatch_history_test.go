//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestHistoryDispatcherCurrentAuthorityAndCapabilitiesBeforeDial(t *testing.T) {
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, _ := acceptanceJournalFixture(t, filepath.Join(t.TempDir(), "unavailable.sock"), account)
	c.Principals[0].Operations = []core.Operation{core.HistoryRead}
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	other := c.Principals[0]
	other.ID = "direct-only"
	other.Targets = []core.Target{{Kind: "direct", Name: "peer"}}
	other.BearerTokenFile = filepath.Join(filepath.Dir(c.StateDir), "other.token")
	if err := auth.CreateCredential(other.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, other)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatch.New(s, v, nil, cursors, []core.Operation{core.HistoryRead}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("history without backend accepted", err)
	}
	d, err := dispatch.New(s, v, b, cursors, []core.Operation{core.HistoryRead})
	if err != nil {
		t.Fatal(err)
	}
	for _, principal := range c.Principals {
		token, err := auth.ReadToken(principal.BearerTokenFile)
		if err != nil {
			t.Fatal(err)
		}
		capabilities, err := d.Capabilities(t.Context(), token)
		if err != nil {
			t.Fatal(err)
		}
		if principal.ID == "direct-only" {
			if len(capabilities.Operations) != 0 || len(capabilities.HistoryTargets) != 0 || capabilities.MaxHistoryBytes != 0 {
				t.Fatal("unsupported DM history advertised", capabilities)
			}
			r := core.Request{Version: 1, RequestID: "direct-history", Operation: core.HistoryRead, Target: &principal.Targets[0], Limit: 1}
			if _, err := d.ReadPage(t.Context(), token, requestJSON(t, r), "", time.Now(), nil); !errors.Is(err, dispatch.ErrUnsupported) {
				t.Fatal("direct history reached unavailable socket", err)
			}
		} else {
			if len(capabilities.HistoryTargets) != 1 || capabilities.MaxHistoryBytes != backend.MaxHistoryBytes {
				t.Fatal("room history missing", capabilities)
			}
			if _, err := s.Revoke(context.Background(), principal.ID); err != nil {
				t.Fatal(err)
			}
			r := core.Request{Version: 1, RequestID: "revoked-history", Operation: core.HistoryRead, Target: &principal.Targets[0], Limit: 1, Cursor: "tampered"}
			if _, err := d.ReadPage(t.Context(), token, requestJSON(t, r), "", time.Now(), nil); !errors.Is(err, auth.ErrForbidden) {
				t.Fatal("revoked history reached cursor decode or socket", err)
			}
		}
	}
}
