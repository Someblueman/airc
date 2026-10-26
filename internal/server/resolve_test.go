package server

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// sendFor posts body from c and returns the stored message's ID, from the
// receipt of a direct message or the echo of a room post.
func sendFor(t *testing.T, c *irc.Client, target, body string) string {
	t.Helper()
	if err := c.Send(target, body); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			switch v := e.(type) {
			case *irc.SendReceiptEvent:
				return v.ID
			case *irc.MessageEvent:
				if v.Message == body && v.Target == target {
					return v.ID
				}
			}
		case <-timeout:
			t.Fatalf("no receipt or echo of %q", body)
		}
	}
}

func mustResolve(t *testing.T, c *irc.Client, r protocol.ChatRequest) irc.ChatEntry {
	t.Helper()
	entries, _, err := handoffChat(t, c, r)
	if err != nil {
		t.Fatalf("%s: %v", r.Action, err)
	}
	if len(entries) != 1 || entries[0].Action != "resolve" || entries[0].Message == nil {
		t.Fatalf("%s answered %+v", r.Action, entries)
	}
	return entries[0]
}

func historySize(srv *Server) int {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.history.size
}

// The author, whoever a message addresses, or an admin may resolve its
// thread; a bystander may not. The resolution replies to the thread's root
// and marks it resolved while it is retained; a repeat posts nothing.
func TestResolveClosesAThreadForAuthorAddresseeOrAdmin(t *testing.T) {
	address, srv := adminWireServer(t)
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")
	stranger := handoffClient(t, address, "stranger", "#work")
	owner := handoffClient(t, address, "owner", "#work")
	if err := owner.AuthenticateAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}

	question := sendFor(t, planner, "#work", "builder: can you review the parser today?")
	if _, _, err := handoffChat(t, stranger, protocol.ChatRequest{Action: "resolve", ID: question}); err == nil || !strings.Contains(err.Error(), "only the author, who it is addressed to, or an admin may resolve this") {
		t.Fatal("a bystander resolved the question:", err)
	}
	if _, _, err := handoffChat(t, stranger, protocol.ChatRequest{Action: "resolve", ID: strings.Repeat("ab", 16)}); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatal("an unknown message was resolved:", err)
	}
	if _, _, err := handoffChat(t, stranger, protocol.ChatRequest{Action: "resolve", ID: "nope"}); err == nil || !strings.Contains(err.Error(), "resolve names the message") {
		t.Fatal("a bad ID was accepted:", err)
	}

	// The mentioned nickname resolves it, naming a reply rather than the
	// root; the resolution replies to the root and tags nobody.
	if err := builder.Reply(question, "looking"); err != nil {
		t.Fatal(err)
	}
	if m := nextMessage(t, reviewer, ""); m.ID != question {
		t.Fatalf("the room saw %+v first", m)
	}
	reply := nextMessage(t, reviewer, "")
	if reply.ReplyTo != question {
		t.Fatalf("the reply %+v", reply)
	}
	resolved := mustResolve(t, builder, protocol.ChatRequest{Action: "resolve", ID: reply.ID, Text: "settled in the DM"})
	if resolved.ID != question || resolved.Target != "#work" || resolved.From != "builder" || resolved.Message.Kind != "resolved" || resolved.Message.ReplyTo != question || resolved.Message.Message != "settled in the DM" || resolved.Message.For != "" {
		t.Fatalf("resolution %+v / %+v", resolved, resolved.Message)
	}
	if m := nextMessage(t, reviewer, "resolved"); m.ReplyTo != question || m.ThreadID != question || m.Message != "settled in the DM" {
		t.Fatalf("the room saw %+v", m)
	}
	if mentions := mentionsOf(srv, resolved.Message.ID); len(mentions) != 0 {
		t.Fatalf("the resolution tagged %v", mentions)
	}
	root, found := func() (Message, bool) { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.history.message(question) }()
	if !found || !root.Resolved {
		t.Fatalf("the root is not marked resolved: %+v", root)
	}
	thread := listing[*irc.HistoryEvent, *irc.EndOfHistoryEvent](t, stranger, "HISTORY thread:"+question)
	if len(thread) != 3 || thread[0].ID != question || !thread[0].Resolved || thread[1].Resolved || thread[2].Kind != "resolved" {
		t.Fatalf("the thread reads %+v", thread)
	}

	// A repeat answers the earlier resolution without posting; the author's
	// own resolution is a post of its own, with the default body.
	size := historySize(srv)
	if again := mustResolve(t, builder, protocol.ChatRequest{Action: "resolve", ID: question, Text: "once more"}); again.Message.ID != resolved.Message.ID || historySize(srv) != size {
		t.Fatalf("the repeat posted: %+v", again)
	}
	byAuthor := mustResolve(t, planner, protocol.ChatRequest{Action: "resolve", ID: question})
	if byAuthor.Message.Message != "Resolved" || byAuthor.Message.ID == resolved.Message.ID {
		t.Fatalf("the author's resolution %+v", byAuthor.Message)
	}

	// A handoff for reviewer is addressed to reviewer by its for, so reviewer
	// may resolve it though the body never names them; an admin may resolve
	// anything.
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", For: "reviewer", Text: "triage the flaky test"})
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "resolve", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "only the author") {
		t.Fatal("someone else resolved the handoff's thread:", err)
	}
	if r := mustResolve(t, reviewer, protocol.ChatRequest{Action: "resolve", ID: offer.ID}); r.ID != offer.ID {
		t.Fatalf("reviewer's resolution %+v", r)
	}
	plain := sendFor(t, builder, "#work", "tests pass on main")
	if r := mustResolve(t, owner, protocol.ChatRequest{Action: "resolve", ID: plain, Text: "noted"}); r.From != "owner" || r.ID != plain {
		t.Fatalf("the admin's resolution %+v", r)
	}
}

// A direct message's recipient resolves it with a reply that reaches the
// sender; a stranger to the conversation may not.
func TestResolveDirectMessageGoesToTheOtherSide(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner")
	builder := handoffClient(t, address, "builder")
	stranger := handoffClient(t, address, "stranger")
	dm := sendFor(t, planner, "builder", "ok to merge?")
	if _, _, err := handoffChat(t, stranger, protocol.ChatRequest{Action: "resolve", ID: dm}); err == nil || !strings.Contains(err.Error(), "only the author") {
		t.Fatal("a stranger resolved a direct message:", err)
	}
	resolved := mustResolve(t, builder, protocol.ChatRequest{Action: "resolve", ID: dm, Text: "merged"})
	if resolved.Target != "planner" || resolved.Message.ReplyTo != dm {
		t.Fatalf("resolution %+v", resolved)
	}
	if m := nextMessage(t, planner, "resolved"); m.From != "builder" || m.Message != "merged" {
		t.Fatalf("the sender saw %+v", m)
	}
}
