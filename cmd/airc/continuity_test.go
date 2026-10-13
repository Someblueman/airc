//go:build darwin || linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A gateway watch starts after a native message ID, returns the room's later
// messages, then waits for live ones; an expired ID starts over with a gap.
func TestGatewayWatchContinuesFromANativeMessageID(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops"}
	var ids []string
	for _, body := range []string{"one", "two", "three"} {
		sent, stderr, err := sendJSON(t, append(common, "--message", body)...)
		if err != nil {
			t.Fatal(err, stderr)
		}
		ids = append(ids, sent.ID)
	}
	fb, _, err := racingClient(options{addr: f.proxy.addr(), nick: "bob"}, nativeOrder)
	if err != nil || fb == nil {
		t.Fatal("no carriers", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	w, err := fb.WatchRoom(ctx, "#ops", ids[0], 50)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	page, err := w.Next(ctx)
	if err != nil || page.Gap || len(page.Messages) != 2 || page.Messages[0].ID != ids[1] || page.Messages[1].ID != ids[2] || w.Last() != ids[2] {
		t.Fatalf("%+v %v", page, err)
	}
	live := make(chan string, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		sent, _, err := sendJSON(t, append(common, "--message", "live")...)
		if err == nil {
			live <- sent.ID
		}
		close(live)
	}()
	var got []string
	for len(got) == 0 {
		page, err := w.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page.Messages {
			got = append(got, m.ID)
		}
	}
	if want := <-live; len(got) != 1 || got[0] != want || w.Last() != want {
		t.Fatalf("live %v, want %s", got, want)
	}
	expired, err := fb.WatchRoom(ctx, "#ops", strings.Repeat("0", 32), 50)
	if err != nil {
		t.Fatal(err)
	}
	defer expired.Close()
	page, err = expired.Next(ctx)
	if err != nil || !page.Gap || len(page.Messages) == 0 || page.Messages[0].ID != ids[0] {
		t.Fatalf("expired start: %+v %v", page, err)
	}
}
