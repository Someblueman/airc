package gateway_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBackendRejectsForeignGrantConfiguration(t *testing.T) {
	for _, mismatch := range []string{"realm", "upstream"} {
		t.Run(mismatch, func(t *testing.T) {
			const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			c, identity := acceptanceJournalFixture(t, filepath.Join(t.TempDir(), "absent.sock"), token)
			ctx := context.Background()
			s, err := store.Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r := core.Request{Version: 1, RequestID: "binding", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not contact another upstream"}
			if _, _, err := s.AdmitAuthor(ctx, identity, r, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			g, err := s.Reserve(ctx, journalScope(t, s, identity), r.RequestID, store.SendAttempt, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if mismatch == "realm" {
				c.Realm = "other-realm"
			} else {
				c.Upstream = &config.Upstream{Network: "unix", Address: filepath.Join(t.TempDir(), "other.sock")}
			}
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			o, err := b.Run(ctx, g)
			// A dial to this absent endpoint would be transient unavailable, not forbidden.
			if err != nil || o.Completion.Failure.Code != core.Forbidden || o.Completion.Failure.Transient || o.Completion.MayHavePosted {
				t.Fatal("foreign grant reached dial or submission", o, err)
			}
		})
	}
}
