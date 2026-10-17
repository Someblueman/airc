//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// A followed check is one stream for its lease: the snapshot first, then a
// result for each live message within milliseconds, empty results while
// quiet, and a renewable end when the lease runs out.
func TestBackendFollowStreamsNewsForItsLease(t *testing.T) {
	socket := fixture(t)
	c, _ := acceptanceJournalFixture(t, socket, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	c.Limits.FollowSeconds, c.Limits.FollowKeepAliveSeconds = 3, 1
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
	reader := store.Submission{Author: core.Author{Nick: "follow-reader"}}
	poster := dial(t, socket, "follow-poster", "", false, true)
	defer poster.Close()
	post := func(room, text string) string {
		t.Helper()
		if err := poster.Send(room, text); err != nil {
			t.Fatal(err)
		}
		return next(t, poster, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}
	first := post("#follow-a", "a one")

	r := core.Request{Version: 1, RequestID: "follow", Operation: core.Check, Limit: 10, Rooms: []core.CheckRoom{{Name: "#follow-a", After: first}, {Name: "#follow-b", After: "recent"}}}
	grant, err := s.AuthorizeAuthorRead(context.Background(), reader, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	type frame struct {
		result core.CheckResult
		at     time.Time
	}
	frames := make(chan frame, 64)
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- b.Follow(context.Background(), grant, backend.CheckScope{DirectMessages: true}, func(result core.CheckResult) error {
			frames <- frame{result, time.Now()}
			return nil
		})
	}()
	take := func() frame {
		t.Helper()
		select {
		case f := <-frames:
			return f
		case <-time.After(5 * time.Second):
			t.Fatal("no frame")
		}
		return frame{}
	}
	// The snapshot at the requested positions: nothing after first, and
	// room b's latest position.
	snapshot := take()
	if len(snapshot.result.Rooms) != 2 || len(snapshot.result.Rooms[0].Messages) != 0 || snapshot.result.Rooms[0].Cursor == "" {
		t.Fatalf("snapshot: %+v", snapshot.result)
	}
	// A post in either room arrives as its own result, promptly.
	second := post("#follow-b", "b one")
	news := take()
	for len(news.result.Rooms[1].Messages) == 0 {
		news = take() // a keepalive may land first
	}
	if news.result.Rooms[1].Messages[0].ID != second || news.result.Rooms[1].Cursor != second || time.Since(start) > 2*time.Second {
		t.Fatalf("news: %+v after %v", news.result, time.Since(start))
	}
	third := post("#follow-a", "a two")
	for {
		f := take()
		if len(f.result.Rooms[0].Messages) == 1 {
			// An idle room's position advances to AIRC's watermark, so a
			// resume there reads nothing twice.
			if f.result.Rooms[0].Messages[0].ID != third || f.result.Rooms[1].Cursor == "" {
				t.Fatalf("second news: %+v", f.result)
			}
			break
		}
	}
	// Quiet: keepalive results carry the positions, no messages.
	quiet := take()
	if len(quiet.result.Rooms[0].Messages) != 0 || quiet.result.Rooms[0].Cursor != third || quiet.result.Rooms[1].Cursor == "" {
		t.Fatalf("keepalive: %+v", quiet.result)
	}
	// The lease ends the stream as renewable.
	select {
	case err := <-done:
		if !errors.Is(err, backend.ErrStreamRenew) {
			t.Fatalf("follow ended with %v", err)
		}
		if elapsed := time.Since(start); elapsed < 2500*time.Millisecond || elapsed > 6*time.Second {
			t.Fatalf("lease ended after %v", elapsed)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the follow outlived its lease")
	}
}

// Over a framed stream, a check with stream: true is a follow: check frames
// then an end frame naming the lease, and a client that resumes from the
// last positions sees nothing twice.
func TestFollowOverAFramedStream(t *testing.T) {
	socket, gatewayToken := currentNative(t)
	c := authorConfig(t, socket, gatewayToken, config.Listener{ID: "framed", Protocol: "framed"})
	c.Limits.FollowSeconds, c.Limits.FollowKeepAliveSeconds = 2, 1
	g := startAuthorGateway(t, c)
	poster := dial(t, socket, "frame-poster", "", false, true)
	defer poster.Close()
	post := func(text string) string {
		t.Helper()
		if err := poster.Send("#framed-follow", text); err != nil {
			t.Fatal(err)
		}
		return next(t, poster, func(e *irc.SendReceiptEvent) bool { return e.Message == text }).ID
	}
	first := post("before")
	dialed, err := dialFramedSuite("tcp")(t, suiteDial{address: g.addresses["framed"]})
	if err != nil {
		t.Fatal(err)
	}
	client := dialed.(*frameSuiteClient)
	defer client.close()
	request := []byte(`{"version":1,"request_id":"f1","operation":"check","limit":10,"rooms":[{"name":"#framed-follow","after":"` + first + `"}],"author":{"nick":"frame-follower"}}`)
	if !client.write(t, "c1", request, true) {
		t.Fatal("write")
	}
	snapshot := client.read(t)
	if snapshot == nil || snapshot.Event != "check" || snapshot.Check == nil || len(snapshot.Check.Rooms[0].Messages) != 0 {
		t.Fatalf("first frame: %+v", snapshot)
	}
	id := post("live")
	var seen *core.CheckResult
	for seen == nil {
		f := client.read(t)
		if f == nil || f.Event != "check" {
			t.Fatalf("frame: %+v", f)
		}
		if len(f.Check.Rooms[0].Messages) > 0 {
			seen = f.Check
		}
	}
	if seen.Rooms[0].Messages[0].ID != id || seen.Rooms[0].Cursor != id {
		t.Fatalf("news: %+v", seen)
	}
	for {
		f := client.read(t)
		if f == nil {
			t.Fatal("stream closed without an end frame")
		}
		if f.Event == "end" {
			if f.Reason != "lease_expired" {
				t.Fatalf("end reason %q", f.Reason)
			}
			break
		}
		if f.Event != "check" || len(f.Check.Rooms[0].Messages) != 0 || f.Check.Rooms[0].Cursor != id {
			t.Fatalf("after the news: %+v", f)
		}
	}
	// Resume from the last position on the same connection: nothing again.
	request = []byte(`{"version":1,"request_id":"f2","operation":"check","limit":10,"rooms":[{"name":"#framed-follow","after":"` + id + `"}],"author":{"nick":"frame-follower"}}`)
	if !client.write(t, "c2", request, true) {
		t.Fatal("write")
	}
	resumed := client.read(t)
	if resumed == nil || resumed.Event != "check" || len(resumed.Check.Rooms[0].Messages) != 0 || resumed.Check.Rooms[0].Cursor != id {
		t.Fatalf("resumed snapshot: %+v", resumed)
	}
}
