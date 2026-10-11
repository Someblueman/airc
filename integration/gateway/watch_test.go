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

func TestWatchNativeLiveAndSnapshotOverlapJoinWithoutJobs(t *testing.T) {
	for _, mode := range []string{"watch", "watch-overlap", "watch-duplicate", "watch-preack"} {
		t.Run(mode, func(t *testing.T) {
			socket := fixture(t)
			const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			author := dial(t, socket, "fixture-author", token, true, true)
			if err := author.SendWithID("#gateway-fixture", "retained root", "watch-root"); err != nil {
				t.Fatal(err)
			}
			root := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "watch-root" })
			proxy, joined, ready, commands := backendProxy(t, socket, mode, nil)
			c, _ := acceptanceJournalFixture(t, proxy, token)
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
			r := core.Request{Version: 1, RequestID: "watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 2, Cursor: root.ID}
			if mode == "watch-overlap" {
				r.Cursor = "*"
			}
			grant, err := s.AuthorizeAuthorRead(t.Context(), identity, r, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			type result struct {
				page backend.HistoryPage
				err  error
			}
			done := make(chan result, 1)
			finished := make(chan struct{})
			go func() { defer close(finished); p, err := b.Read(ctx, grant, nil); done <- result{p, err} }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(2 * time.Second):
					t.Error("backend watch caller survived cleanup")
				}
				select {
				case <-joined:
				case <-time.After(2 * time.Second):
					t.Error("watch work survived cleanup")
				}
			})
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("watch snapshot never completed")
			}
			if err := author.ReplyWithID(root.ID, "live reply\nwhole body", "watch-reply"); err != nil {
				t.Fatal(err)
			}
			reply := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "watch-reply" })
			select {
			case got := <-done:
				count := 1
				if mode == "watch-overlap" {
					count = 2
				}
				if got.err != nil || len(got.page.Messages) != count || got.page.Cursor != reply.ID {
					t.Fatal("watch lost live update", got)
				}
				m := got.page.Messages[count-1]
				if m.Message != reply.Message || m.ReplyTo != root.ID || m.ThreadID != root.ID || m.Receipt != nil {
					t.Fatal("live metadata changed or receipt invented", m)
				}
				if (mode == "watch-duplicate" || mode == "watch-preack") && m.Seq == 0 {
					t.Fatal("snapshot/live overlap discarded stored sequence", m)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("live event did not wake watch")
			}
			select {
			case <-joined:
			case <-time.After(2 * time.Second):
				t.Fatal("watch lease survived return")
			}
			if commands.Load() != 1 {
				t.Fatal("watch retried snapshot", commands.Load())
			}
			if _, err := s.LookupAuthor(t.Context(), identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("watch created delivery job", err)
			}
		})
	}
}
