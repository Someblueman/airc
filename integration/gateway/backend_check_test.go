package gateway_test

import (
	"context"
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
		result, err := b.Check(ctx, grant)
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
