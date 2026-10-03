package server

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

type retryModelEntry struct {
	owner, request, id, target, body string
	seq                              uint64
}

// Exercise the actual post/recovery handlers with already authenticated session
// identities. A list models only the retained requests; no production key/index
// helpers are used. Reuse after eviction is a new post, not eternal deduplication.
func FuzzRetainedRequestRecovery(f *testing.F) {
	f.Add(uint8(3), []byte{0, 0, 1, 2, 3, 8, 9, 0, 16, 17, 24, 25, 0, 1})
	f.Add(uint8(1), []byte{0, 2, 1, 16, 16, 17, 128, 129, 64, 65})
	f.Fuzz(func(t *testing.T, capacity uint8, ops []byte) {
		if len(ops) > 96 {
			t.Skip()
		}
		limit := 1 + int(capacity%8)
		s := New(Config{HistoryLimit: limit, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		var model []retryModelEntry
		accepted := uint64(0)
		allIDs := map[string]bool{}
		for step, op := range ops {
			nick := []string{"writer", "WRITER", "other", "renamed"}[int(op/32)%4]
			account := []string{"", "account-a", "account-b"}[int(op/16)%3]
			owner := "account:" + account
			if account == "" {
				owner = "guest:" + strings.ToLower(nick)
			}
			request := fmt.Sprintf("r%d", (op/2)%3)
			target := []string{"#room", "#ROOM", "peer", "PEER"}[int(op/8)%4]
			body := fmt.Sprintf("original %d é🐈\nsecond line", op/64)
			actor := &session{client: Client{Nick: nick, Username: nick}, accountID: account, ephemeral: true, out: make(chan string, 8), done: make(chan struct{})}
			var old *retryModelEntry
			for i := range model {
				if model[i].owner == owner && model[i].request == request {
					copy := model[i]
					old = &copy
					break
				}
			}
			retry := op&1 != 0
			if retry {
				s.retryRequestLocked(actor, protocol.Command{Params: []string{request}})
			} else {
				s.messageLocked(actor, protocol.Command{Name: "PRIVMSG", Params: []string{target}, Trailing: body, Tags: map[string]string{protocol.RequestIDTag: request, protocol.BodyTag: protocol.EncodeBody(body)}}, false, nil)
			}
			if len(actor.out) != 1 {
				t.Fatalf("step %d: expected one receipt/rejection, got %d", step, len(actor.out))
			}
			wire, err := protocol.Parse(<-actor.out)
			if err != nil {
				t.Fatal(err)
			}
			sameTarget := old != nil && (old.target == target || !strings.HasPrefix(target, "#") && strings.EqualFold(old.target, target))
			switch {
			case retry && old == nil:
				if wire.Name != "488" {
					t.Fatal("unknown recovery must not post", wire)
				}
			case !retry && old != nil && (!sameTarget || old.body != body):
				if wire.Name != "487" {
					t.Fatal("conflicting request reuse must fail", wire)
				}
			default:
				if wire.Name != "762" {
					t.Fatal("expected acceptance", wire)
				}
				receipt, err := protocol.DecodeMessageMetadata(wire.Trailing)
				if err != nil || receipt.Receipt == nil || !receipt.Receipt.Accepted || receipt.Receipt.Persisted {
					t.Fatal("invalid memory-only receipt", receipt, err)
				}
				if old != nil {
					if receipt.ID != old.id || receipt.Seq != old.seq || receipt.Message != old.body || receipt.Target != old.target {
						t.Fatal("recovery changed accepted content/ID", receipt)
					}
				} else {
					accepted++
					if allIDs[receipt.ID] || receipt.ID == "" || receipt.Seq != accepted || receipt.RequestID != request || receipt.AccountID != account || receipt.Message != body || receipt.Target != target {
						t.Fatal("new acceptance differs", receipt)
					}
					allIDs[receipt.ID] = true
					model = append(model, retryModelEntry{owner, request, receipt.ID, target, body, accepted})
					if len(model) > limit {
						model = model[1:]
					}
				}
			}
			if s.seq != accepted || s.history.size != len(model) {
				t.Fatal("retry/conflict changed acceptance count")
			}
			for i, want := range model {
				got := s.history.at(i)
				if got.ID != want.id || got.Body != want.body || got.Target != want.target || got.Seq != want.seq {
					t.Fatal("history changed outside an accepted new post")
				}
			}
		}
	})
}
