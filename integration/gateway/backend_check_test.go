package gateway_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestBackendCheckRoomsAndWait reads two rooms after positions in one
// check, then waits: a live post in either room ends the wait with it, and
// a quiet wait ends empty at its bound.
func TestBackendCheckRoomsAndWait(t *testing.T) {
	socket := fixture(t)
	c, _ := acceptanceJournalFixture(t, socket, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reader := store.Submission{Author: core.Author{Nick: "check-reader"}}
	poster := dial(t, socket, "check-poster", "", false, true)
	defer poster.Close()
	post := func(room, text string) string {
		t.Helper()
		if err := poster.Send(room, text); err != nil {
			t.Fatal(err)
		}
		return next(t, poster, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}
	first := post("#check-a", "a one")
	post("#check-b", "b one")
	check := func(r core.Request) (core.CheckResult, time.Duration) {
		t.Helper()
		r.Version, r.RequestID, r.Operation = 1, "check", core.Check
		grant, err := s.AuthorizeAuthorRead(context.Background(), reader, r, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		start := time.Now()
		result, err := b.Check(ctx, grant, backend.CheckScope{DirectMessages: true})
		if err != nil {
			t.Fatal(err)
		}
		return result, time.Since(start)
	}
	result, _ := check(core.Request{Limit: 10, Rooms: []core.CheckRoom{{Name: "#check-a", After: first}, {Name: "#check-b", After: "retained"}}})
	if len(result.Rooms) != 2 || len(result.Rooms[0].Messages) != 0 || len(result.Rooms[1].Messages) != 1 || result.Rooms[1].Messages[0].Message != "b one" {
		t.Fatalf("check pages: %+v", result)
	}
	latest := result.Rooms[1].Cursor
	rooms := []core.CheckRoom{{Name: "#check-a", After: latest}, {Name: "#check-b", After: latest}}

	sent := make(chan error, 1)
	go func() {
		time.Sleep(400 * time.Millisecond)
		sent <- poster.Send("#check-a", "a two")
	}()
	result, took := check(core.Request{Limit: 10, WaitSeconds: 10, Rooms: rooms})
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if len(result.Rooms[0].Messages) != 1 || result.Rooms[0].Messages[0].Message != "a two" || took < 300*time.Millisecond || took > 2*time.Second {
		t.Fatalf("waiting check: %+v after %v", result, took)
	}

	// Nothing new: the wait ends before the 4 s request deadline, empty.
	rooms = []core.CheckRoom{{Name: "#check-a", After: result.Rooms[0].Cursor}, {Name: "#check-b", After: result.Rooms[0].Cursor}}
	result, took = check(core.Request{Limit: 10, WaitSeconds: 10, Rooms: rooms})
	if len(result.Rooms[0].Messages)+len(result.Rooms[1].Messages) != 0 || took < 2*time.Second || took > 4*time.Second {
		t.Fatalf("quiet check: %+v after %v", result, took)
	}
}

// TestBackendCheckLongWait waits longer than an upstream session's 10 s read
// timeout with nothing to read: keepalive pings hold the session, and the
// quiet check answers empty instead of failing.
func TestBackendCheckLongWait(t *testing.T) {
	socket := fixture(t)
	c, _ := acceptanceJournalFixture(t, socket, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := core.Request{Version: 1, RequestID: "long", Operation: core.Check, Limit: 5, WaitSeconds: 12, Rooms: []core.CheckRoom{{Name: "#quiet-room"}}}
	grant, err := s.AuthorizeAuthorRead(context.Background(), store.Submission{Author: core.Author{Nick: "long-reader"}}, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Second)
	defer cancel()
	start := time.Now()
	result, err := b.Check(ctx, grant, backend.CheckScope{DirectMessages: true})
	if err != nil || len(result.Rooms) != 1 || len(result.Rooms[0].Messages) != 0 || time.Since(start) < 11*time.Second {
		t.Fatal("long quiet check", result, err, time.Since(start))
	}
}

// TestBackendCheckInbox reads a registered author's inbox: the direct
// message AIRC recorded as received by its account, and a mention in a room
// its listener lets it read. A message to the same nickname from before the
// account existed is left out; a guest has no inbox.
func TestBackendCheckInbox(t *testing.T) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, _ := acceptanceJournalFixture(t, socket, token)
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sender := dial(t, socket, "inbox-sender", "", false, true)
	send := func(target, text string) string {
		t.Helper()
		if err := sender.Send(target, text); err != nil {
			t.Fatal(err)
		}
		return next(t, sender, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}
	send("inbox-owner", "before the account")
	registered := dial(t, socket, "inbox-owner", token, true, true)
	_ = registered.Close()
	send("inbox-owner", "for the account")
	send("#inbox-room", "inbox-owner: a mention")

	check := func(sub store.Submission, readable func(string) bool) (core.CheckResult, error) {
		r := core.Request{Version: 1, RequestID: "inbox", Operation: core.Check, Limit: 10, Inbox: &core.CheckInbox{After: "retained"}}
		grant, err := s.AuthorizeAuthorRead(context.Background(), sub, r, time.Now())
		if err != nil {
			return core.CheckResult{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return b.Check(ctx, grant, backend.CheckScope{Readable: readable, DirectMessages: true})
	}
	owner := registeredSubmission(t, "inbox-owner", token)
	// The listener lets the author read #inbox-room: its mention is inbox news.
	result, err := check(owner, func(room string) bool { return room == "#inbox-room" })
	if err != nil || result.Inbox == nil || result.Inbox.Room != "@inbox-owner" || len(result.Inbox.Messages) != 2 || result.Inbox.Messages[0].Message != "for the account" || result.Inbox.Messages[1].Message != "inbox-owner: a mention" || len(result.Rooms) != 0 {
		t.Fatalf("inbox: %+v %v", result.Inbox, err)
	}
	// A room the listener does not allow stays out of the inbox.
	result, err = check(owner, func(string) bool { return false })
	if err != nil || result.Inbox == nil || len(result.Inbox.Messages) != 1 || result.Inbox.Messages[0].Message != "for the account" {
		t.Fatalf("inbox without the room: %+v %v", result.Inbox, err)
	}
	// A waiting inbox check answers on the next direct message.
	r := core.Request{Version: 1, RequestID: "inbox-wait", Operation: core.Check, Limit: 10, WaitSeconds: 10, Inbox: &core.CheckInbox{After: result.Inbox.Cursor}}
	grant, err := s.AuthorizeAuthorRead(context.Background(), owner, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() {
		time.Sleep(400 * time.Millisecond)
		sent <- sender.Send("inbox-owner", "while waiting")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	start := time.Now()
	result, err = b.Check(ctx, grant, backend.CheckScope{DirectMessages: true})
	cancel()
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if err != nil || len(result.Inbox.Messages) != 1 || result.Inbox.Messages[0].Message != "while waiting" || time.Since(start) > 2*time.Second {
		t.Fatalf("waiting inbox: %+v %v after %v", result.Inbox, err, time.Since(start))
	}

	// A guest's inbox has its mentions in readable rooms, never its direct
	// messages: nothing proves who received those.
	send("inbox-guest", "to a guest")
	send("#inbox-room", "inbox-guest: a guest mention")
	result, err = check(store.Submission{Author: core.Author{Nick: "inbox-guest"}}, func(string) bool { return true })
	if err != nil || result.Inbox == nil || !result.Inbox.MentionsOnly || len(result.Inbox.Messages) != 1 || result.Inbox.Messages[0].Message != "inbox-guest: a guest mention" {
		t.Fatalf("guest inbox: %+v %v", result.Inbox, err)
	}
}

// TestBackendCheckThreads reads followed threads in a check: a thread in a
// readable room comes whole, a thread in a direct conversation is withheld,
// and a thread AIRC does not retain is expired.
func TestBackendCheckThreads(t *testing.T) {
	socket := fixture(t)
	c, _ := acceptanceJournalFixture(t, socket, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sender := dial(t, socket, "thread-sender", "", false, true)
	receipt := func(text string) string {
		t.Helper()
		return next(t, sender, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}
	if err := sender.Send("#thread-room", "room root"); err != nil {
		t.Fatal(err)
	}
	room := receipt("room root")
	if err := sender.Reply(room, "room child"); err != nil {
		t.Fatal(err)
	}
	receipt("room child")
	if err := sender.Send("thread-peer", "dm root"); err != nil {
		t.Fatal(err)
	}
	direct := receipt("dm root")
	if err := sender.Reply(direct, "dm child"); err != nil {
		t.Fatal(err)
	}
	receipt("dm child")
	missing := strings.Repeat("e", 32)

	r := core.Request{Version: 1, RequestID: "threads", Operation: core.Check, Limit: 10, Threads: []core.CheckThread{{ID: room, After: "retained"}, {ID: direct, After: "retained"}, {ID: missing, After: "retained"}}}
	grant, err := s.AuthorizeAuthorRead(context.Background(), store.Submission{Author: core.Author{Nick: "thread-reader"}}, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	result, err := b.Check(ctx, grant, backend.CheckScope{Readable: func(room string) bool { return room == "#thread-room" }})
	if err != nil || len(result.Threads) != 3 {
		t.Fatal(result, err)
	}
	if page := result.Threads[0]; page.Room != "thread:"+room || page.Status != "ok" || len(page.Messages) != 2 || page.Messages[1].Message != "room child" {
		t.Fatalf("room thread: %+v", page)
	}
	if page := result.Threads[1]; page.Status != "withheld" || len(page.Messages) != 0 || page.Cursor != "" {
		t.Fatalf("direct thread: %+v", page)
	}
	if page := result.Threads[2]; page.Status != "expired" || len(page.Messages) != 0 {
		t.Fatalf("missing thread: %+v", page)
	}
	// A waiting check observes the threads, the expired one included, and
	// wakes on a reply in the room thread.
	r.RequestID, r.WaitSeconds = "threads-wait", 10
	r.Threads = []core.CheckThread{{ID: room, After: result.Threads[0].Cursor}, {ID: missing}}
	grant, err = s.AuthorizeAuthorRead(context.Background(), store.Submission{Author: core.Author{Nick: "thread-reader"}}, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() {
		time.Sleep(400 * time.Millisecond)
		sent <- sender.Reply(room, "late child")
	}()
	ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	result, err = b.Check(ctx, grant, backend.CheckScope{Readable: func(string) bool { return true }})
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if err != nil || len(result.Threads) != 2 || len(result.Threads[0].Messages) != 1 || result.Threads[0].Messages[0].Message != "late child" || time.Since(start) > 2*time.Second {
		t.Fatalf("waiting thread check: %+v %v after %v", result, err, time.Since(start))
	}
}
