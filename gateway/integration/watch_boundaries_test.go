package gateway_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
)

func TestWatchNoiseCannotExtendIdlePollAndDisabledCheckNeverSubscribes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"watch-noise", "watch-no-check"} {
		t.Run(mode, func(t *testing.T) {
			socket := fixture(t)
			const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			provision := dial(t, socket, "fixture-author", account, true, true)
			_ = provision.Close()
			proxy, joined, _, commands := backendProxy(t, socket, mode, nil)
			c, _ := acceptanceJournalFixture(t, proxy, account)
			identity := fixtureIdentity(t, c)
			s, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			b, err := backend.New(c)
			if err != nil {
				t.Fatal(err)
			}
			r := core.Request{Version: 1, RequestID: "watch-boundary", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
			grant, err := s.AuthorizeAuthorRead(t.Context(), identity, r, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			page, err := b.Read(t.Context(), grant, nil)
			if mode == "watch-no-check" {
				if !errors.Is(err, backend.ErrReadUnsupported) || commands.Load() != 0 || len(page.Messages) != 0 {
					t.Fatal("disabled CHECK used observation/read command", page, err, commands.Load())
				}
			} else if err != nil || time.Since(start) > 4*time.Second || page.Status != "ok" || page.Gap || len(page.Messages) != 0 || page.Cursor != "" || commands.Load() != 1 {
				t.Fatal("noise extended or corrupted idle poll", page, err, time.Since(start), commands.Load())
			}
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("watch boundary left lease work")
			}
		})
	}
}
