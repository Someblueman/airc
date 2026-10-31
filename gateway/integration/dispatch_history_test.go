//go:build darwin || linux

package gateway_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
)

func TestHistoryDispatcherListenerPolicyAndCapabilitiesBeforeDial(t *testing.T) {
	t.Parallel()
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, _ := acceptanceJournalFixture(t, filepath.Join(t.TempDir(), "unavailable.sock"), account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
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
	room := config.Listener{ID: "room", Protocol: "http", Operations: []core.Operation{core.HistoryRead}, Targets: []core.Target{{Kind: "room", Name: "#gateway-fixture"}}}
	if _, err := dispatch.ForListener(s, nil, cursors, room); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("history without backend accepted", err)
	}
	direct := room
	direct.ID, direct.Targets = "direct-only", []core.Target{{Kind: "direct", Name: "peer"}}
	for _, l := range []config.Listener{room, direct} {
		d, err := dispatch.ForListener(s, b, cursors, l)
		if err != nil {
			t.Fatal(err)
		}
		discovery := core.Request{Version: 1, RequestID: "caps", Operation: core.CapabilitiesGet}
		result, err := d.Submit(t.Context(), dispatch.Ingress{}, []byte(authorRequest(t, discovery, fixtureAuthor, account)), time.Now())
		if err != nil || result.Capabilities == nil {
			t.Fatal(err)
		}
		capabilities := result.Capabilities
		if l.ID == "direct-only" {
			if slices.Contains(capabilities.Operations, core.HistoryRead) || len(capabilities.HistoryTargets) != 0 || capabilities.MaxHistoryBytes != 0 {
				t.Fatal("unsupported DM history advertised", capabilities)
			}
			r := core.Request{Version: 1, RequestID: "direct-history", Operation: core.HistoryRead, Target: &direct.Targets[0], Limit: 1}
			if _, err := d.Submit(t.Context(), dispatch.Ingress{}, []byte(authorRequest(t, r, fixtureAuthor, account)), time.Now()); !errors.Is(err, dispatch.ErrUnsupported) {
				t.Fatal("direct history reached unavailable socket", err)
			}
		} else if len(capabilities.HistoryTargets) != 1 || capabilities.MaxHistoryBytes != backend.MaxHistoryBytes {
			t.Fatal("room history missing", capabilities)
		}
	}
}
