package fallback

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRoomReaderCheck reads rooms in one check, bounding the wait it asks
// for by the call's timeout, and reads room by room from a gateway that has
// no check.
func TestRoomReaderCheck(t *testing.T) {
	for _, noCheck := range []bool{false, true} {
		g := newFakeGateway(t)
		g.noCheck = noCheck
		c := &Client{Host: "127.0.0.1", Nick: "agent", Endpoints: []Endpoint{g.endpoint(t)}}
		reader, err := c.ReadRooms(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		after := strings.Repeat("a", 32)
		rooms := []RoomAfter{{Room: "#quiet", After: after}, {Room: "#busy", After: after}}
		pages, err := reader.Check(context.Background(), rooms, 10, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_ = reader.Close()
		seen := g.seen()
		if noCheck {
			// The refused check, then a history read per room.
			if reader.Waits() || len(seen) != 3 || seen[1].Operation != "history.read" || seen[2].Target.Name != "#busy" || len(pages) != 2 || len(pages[1].Messages) != 1 {
				t.Fatalf("per-room fallback: %+v %+v", seen, pages)
			}
			continue
		}
		if !reader.Waits() || len(seen) != 1 || seen[0].Operation != "check" || len(seen[0].Rooms) != 2 || seen[0].Rooms[1].After != after {
			t.Fatalf("check request: %+v", seen)
		}
		// The default call timeout is 6 s, so a minute's wait asks for 4 s.
		if seen[0].WaitSeconds != 4 {
			t.Fatalf("check asked to wait %d s", seen[0].WaitSeconds)
		}
		if len(pages) != 2 || len(pages[0].Messages) != 0 || len(pages[1].Messages) != 1 || pages[1].Messages[0].Message != "news" {
			t.Fatalf("check pages: %+v", pages)
		}
	}
}

// TestRoomReaderCheckInbox reads the author's inbox with its rooms in one
// check, and none from a gateway without check.
func TestRoomReaderCheckInbox(t *testing.T) {
	for _, noCheck := range []bool{false, true} {
		g := newFakeGateway(t)
		g.noCheck = noCheck
		c := &Client{Host: "127.0.0.1", Nick: "agent", Token: strings.Repeat("f", 64), Endpoints: []Endpoint{g.endpoint(t)}}
		reader, err := c.ReadRooms(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		after := strings.Repeat("a", 32)
		pages, inbox, err := reader.CheckInbox(context.Background(), []RoomAfter{{Room: "#busy", After: after}}, after, 10, 0)
		_ = reader.Close()
		if err != nil || len(pages) != 1 || len(pages[0].Messages) != 1 {
			t.Fatal("rooms", pages, err)
		}
		if noCheck {
			if inbox != nil {
				t.Fatal("a gateway without check served an inbox", inbox)
			}
			continue
		}
		seen := g.seen()
		if inbox == nil || len(inbox.Messages) != 1 || inbox.Messages[0].Message != "for you" || seen[0].Inbox == nil || seen[0].Inbox.After != after {
			t.Fatalf("inbox: %+v %+v", inbox, seen)
		}
	}
}
