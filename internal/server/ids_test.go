package server

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// idWith is a 32-character ID that starts with prefix and continues with
// fill, so tests make IDs that share exactly as much as they mean to.
func idWith(prefix string, fill byte) string {
	return prefix + strings.Repeat(string(fill), 32-len(prefix))
}

// An abbreviated ID resolves among everything the server retains under that
// kind of ID: the window and its index, pins, polls, the ledgers and the
// paste index; only an abbreviation that two of them match is refused.
func TestResolveIDAmongRetainedStores(t *testing.T) {
	s := New(Config{HistoryLimit: 5})
	if err := s.RestoreChat(filepath.Join(t.TempDir(), "chat")); err != nil {
		t.Fatal(err)
	}
	// Two retained messages share eight characters and part at the ninth.
	a, b := idWith("aaaaaaaa0", '1'), idWith("aaaaaaaa2", '3')
	// A thread root evicted while its reply is retained is known to the index.
	root, reply := idWith("bbbbbbbb", '0'), idWith("bbbbbbbb", '1')
	s.history.add(Message{ID: root, From: "x", Target: "#room", Body: "root"})
	s.history.add(Message{ID: a, From: "x", Target: "#room", Body: "a"})
	s.history.add(Message{ID: b, From: "x", Target: "#room", Body: "b"})
	s.history.add(Message{ID: reply, ReplyTo: root, ThreadID: root, From: "y", Target: "#room", Body: "reply"})
	s.history.add(Message{ID: idWith("cccccccc", '0'), From: "x", Target: "#room", Body: "c"})
	pin, pollID := idWith("dddddddd", '0'), idWith("eeeeeeee", '0')
	s.chat.Pins["#room"] = []Message{{ID: pin, From: "x", Target: "#room", Body: "pinned"}}
	s.chat.Polls[pollID] = poll{Message: Message{ID: pollID, Kind: "poll"}}
	offer, ask := idWith("ffffffff", '0'), idWith("12345678", '0')
	s.handoffs.all[offer] = handoff{Offer: Message{ID: offer}, State: handoffOpen}
	s.decisions.all[ask] = decision{Offer: Message{ID: ask}, State: decisionOpen}
	// A handoff also retained in the window is one ID, not an ambiguity;
	// it is the sixth message, which evicts the root.
	s.history.add(Message{ID: offer, From: "x", Target: "#room", Body: "offer"})
	if _, found := s.history.message(root); found {
		t.Fatal("the root should have been evicted")
	}
	paste := idWith("abcdef12", '0')
	s.pastes = map[string]pasteRecord{paste: {ID: paste}}

	for _, tc := range []struct {
		arg   string
		store idStore
		want  string
	}{
		{a[:9], messageIDStore, a},
		{b[:20], messageIDStore, b},
		{a, messageIDStore, a},
		{reply[:9], messageIDStore, reply},
		{root[:9], messageIDStore, root},
		{pin[:8], messageIDStore, pin},
		{pollID[:8], messageIDStore, pollID},
		{offer[:8], messageIDStore, offer},
		{ask[:8], messageIDStore, ask},
		{paste[:8], pasteIDStore, paste},
		// Nothing matches: the argument goes through as given, for the
		// command's own "not retained".
		{"0123456789ab", messageIDStore, "0123456789ab"},
		{paste[:8], messageIDStore, paste[:8]},
		{a[:9], pasteIDStore, a[:9]},
		{idWith("98765432", '0'), messageIDStore, idWith("98765432", '0')},
		// Too short for an abbreviation, or not an ID at all: unchanged.
		{a[:7], messageIDStore, a[:7]},
		{"last", messageIDStore, "last"},
		{"", messageIDStore, ""},
	} {
		got, err := s.resolveIDLocked(tc.arg, tc.store)
		if err != nil || got != tc.want {
			t.Errorf("resolve(%q, %d) = %q, %v; want %q", tc.arg, tc.store, got, err, tc.want)
		}
	}
	for _, arg := range []string{a[:8], root[:8]} {
		_, err := s.resolveIDLocked(arg, messageIDStore)
		if !errors.As(err, new(ambiguousIDError)) || !strings.Contains(err.Error(), "ambiguous ID prefix "+arg+"; give more of it") {
			t.Errorf("resolve(%q) = %v, want an ambiguity", arg, err)
		}
	}
}

// sent is what a reply or reaction was recorded as: its receipt's links, or
// those of its echo to a persistent session.
type sent struct{ replyTo, threadID, reaction string }

// awaitSend returns the links of the reply or reaction c just sent, or the
// server's refusal of it.
func awaitSend(t *testing.T, c *irc.Client) (sent, *irc.RejectedError) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			switch v := e.(type) {
			case *irc.SendReceiptEvent:
				return sent{v.ReplyTo, v.ThreadID, v.Reaction}, nil
			case *irc.MessageEvent:
				if v.From == c.Nick() {
					return sent{v.ReplyTo, v.ThreadID, v.Reaction}, nil
				}
			case *irc.RawEvent:
				var rejected *irc.RejectedError
				if errors.As(v.Rejection(), &rejected) {
					return sent{}, rejected
				}
			}
		case <-timeout:
			t.Fatal("no receipt or refusal")
		}
	}
}

// Every command that takes an ID accepts its first eight or more characters
// and answers with the full ID; an ambiguous one is refused with 461.
func TestAbbreviatedIDsOnTheWire(t *testing.T) {
	dir := t.TempDir()
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
		if err := s.RestoreChat(filepath.Join(dir, "chat")); err != nil {
			return err
		}
		return s.RestorePastes(filepath.Join(dir, "pastes.json"))
	})
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	question := sendFor(t, planner, "#work", "builder: which branch?")
	short := question[:8]

	// REPLY and REACT name the parent by its abbreviation; receipts carry
	// the full ID.
	if err := builder.Reply(short, "work-7"); err != nil {
		t.Fatal(err)
	}
	reply, refused := awaitSend(t, builder)
	if refused != nil || reply.replyTo != question || reply.threadID != question {
		t.Fatalf("reply by prefix: %+v %v", reply, refused)
	}
	if err := builder.React(question[:12], "seen"); err != nil {
		t.Fatal(err)
	}
	if reaction, refused := awaitSend(t, builder); refused != nil || reaction.replyTo != question || reaction.reaction != "seen" {
		t.Fatalf("reaction by prefix: %+v %v", reaction, refused)
	}
	// HISTORY and the thread selectors.
	if thread := listing[*irc.HistoryEvent, *irc.EndOfHistoryEvent](t, planner, "HISTORY thread:"+short+" 10 *"); len(thread) != 3 || thread[0].ID != question {
		t.Fatalf("thread by prefix %+v", thread)
	}
	if replies := listing[*irc.HistoryEvent, *irc.EndOfHistoryEvent](t, planner, "HISTORY replies:"+short+" 10 *"); len(replies) != 1 || replies[0].ReplyTo != question || replies[0].Message != "work-7" {
		t.Fatalf("replies by prefix %+v", replies)
	}
	// CHAT actions: the id field.
	entries, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "context", ID: short, Limit: 10})
	if err != nil || len(entries) == 0 || entries[len(entries)-1].Context == nil || entries[len(entries)-1].Context.TriggerID != question {
		t.Fatalf("context by prefix: %+v %v", entries, err)
	}
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "finish task 7"})
	if accepted := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID[:8]}); accepted.ID != offer.ID || accepted.State != "accepted" {
		t.Fatalf("accept by prefix %+v", accepted)
	}
	if listed := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoffs", ID: offer.ID[:10]}); listed.ID != offer.ID {
		t.Fatalf("handoffs by prefix %+v", listed)
	}
	info := mustUpload(t, planner, "note.txt", []byte("hello, paste\n"))
	entries, _, err = handoffChat(t, builder, protocol.ChatRequest{Action: "paste-get", ID: info.ID[:8]})
	if err != nil || len(entries) != 1 || entries[0].ID != info.ID || entries[0].Paste == nil {
		t.Fatalf("paste-get by prefix: %+v %v", entries, err)
	}
	entries, _, err = handoffChat(t, builder, protocol.ChatRequest{Action: "pastes", ID: info.ID[:8]})
	if err != nil || len(entries) != 1 || entries[0].ID != info.ID {
		t.Fatalf("pastes by prefix: %+v %v", entries, err)
	}
	// A paste's ID is not a message's: the same prefix finds nothing there.
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "context", ID: info.ID[:8], Limit: 10}); err == nil || !strings.Contains(err.Error(), "no longer retained") {
		t.Fatalf("a paste ID as a message: %v", err)
	}

	// Two retained messages sharing eight characters make that prefix
	// ambiguous everywhere an ID is read; nine resolve it.
	twinA, twinB := idWith("0a0a0a0a", 'b'), idWith("0a0a0a0a", 'c')
	srv.mu.Lock()
	srv.history.add(Message{ID: twinA, From: "planner", Target: "#work", Body: "twin a", Timestamp: time.Now()})
	srv.history.add(Message{ID: twinB, From: "planner", Target: "#work", Body: "twin b", Timestamp: time.Now()})
	srv.mu.Unlock()
	want := "ambiguous ID prefix 0a0a0a0a; give more of it"
	if err := builder.Reply("0a0a0a0a", "which twin?"); err != nil {
		t.Fatal(err)
	}
	if _, refused = awaitSend(t, builder); refused == nil || refused.Code != "461" || refused.Message != want {
		t.Fatalf("an ambiguous reply parent: %v", refused)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "context", ID: "0a0a0a0a", Limit: 10}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("an ambiguous context id: %v", err)
	}
	var rejected *irc.RejectedError
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: "0a0a0a0a"}); !errors.As(err, &rejected) || rejected.Code != "461" || rejected.Message != want {
		t.Fatalf("an ambiguous handoff id: %v", err)
	}
	conn, reader := rawConn(t, address)
	for _, line := range []string{"NICK reader\r\n", "USER reader 0 * :reader\r\n"} {
		if _, err := conn.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	expectLine(t, reader, conn, " 001 ")
	if _, err := conn.Write([]byte("HISTORY thread:0a0a0a0a 10 *\r\n")); err != nil {
		t.Fatal(err)
	}
	if line := expectLine(t, reader, conn, "0a0a0a0a"); !strings.Contains(line, " 461 ") || !strings.Contains(line, want) {
		t.Fatalf("an ambiguous thread selector: %s", line)
	}
	if thread := listing[*irc.HistoryEvent, *irc.EndOfHistoryEvent](t, planner, "HISTORY thread:0a0a0a0ab 10 *"); len(thread) != 1 || thread[0].ID != twinA {
		t.Fatalf("nine characters resolve the twin: %+v", thread)
	}
	// An abbreviation nothing matches is an unknown ID, worded as before.
	if err := builder.Reply("fedcba9876", "nobody"); err != nil {
		t.Fatal(err)
	}
	if _, refused = awaitSend(t, builder); refused == nil || refused.Code != "430" || !strings.Contains(refused.Message, "not retained") {
		t.Fatalf("an unknown reply parent: %v", refused)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: "fedcba9876"}); err == nil || !strings.Contains(err.Error(), "handoff is not retained") {
		t.Fatalf("an unknown handoff: %v", err)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "paste-get", ID: "fedcba9876"}); err == nil || !strings.Contains(err.Error(), "paste is not retained") {
		t.Fatalf("an unknown paste: %v", err)
	}
	// Seven characters are not an abbreviation.
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "context", ID: question[:7], Limit: 10}); err == nil || !strings.Contains(err.Error(), "requires a message ID") {
		t.Fatalf("a seven-character id: %v", err)
	}
}
