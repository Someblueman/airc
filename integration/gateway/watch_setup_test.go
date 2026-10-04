package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWatchMissingOrRejectedAcknowledgementDiscardsBufferedLiveData(t *testing.T) {
	for _, mode := range []string{"watch-noack", "watch-rejectack"} {
		t.Run(mode, func(t *testing.T) {
			socket := fixture(t)
			const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			author := dial(t, socket, "fixture-author", account, true, true)
			proxy, joined, ready, commands := backendProxy(t, socket, mode, nil)
			c, _ := acceptanceJournalFixture(t, proxy, account)
			c.Principals[0].Operations = append(c.Principals[0].Operations, core.Watch)
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
			r := core.Request{Version: 1, RequestID: "watch-setup", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
			g, err := s.AuthorizeRead(t.Context(), identity, r, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			type result struct {
				page backend.HistoryPage
				err  error
			}
			done := make(chan result, 1)
			finished := make(chan struct{})
			go func() { defer close(finished); page, err := b.Read(ctx, g, nil); done <- result{page, err} }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("setup caller survived cleanup")
				}
			})
			select {
			case <-ready:
			case <-time.After(time.Second):
				t.Fatal("observation never requested")
			}
			if err := author.SendWithID("#gateway-fixture", "pre-ack buffered live", "setup-live"); err != nil {
				t.Fatal(err)
			}
			next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "setup-live" })
			select {
			case got := <-done:
				if !errors.Is(got.err, backend.ErrReadUnavailable) || len(got.page.Messages) != 0 || got.page.Cursor != "" || commands.Load() != 0 {
					t.Fatal("incomplete setup exposed data or began snapshot", got, commands.Load())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("unacknowledged watch did not stop")
			}
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("failed setup left subscription work")
			}
		})
	}
}
