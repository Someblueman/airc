//go:build darwin || linux

package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// A gateway read starts after a native message ID and returns the room's
// later messages; an expired ID starts over with a gap.
func TestGatewayReadContinuesFromANativeMessageID(t *testing.T) {
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
	reader, err := fb.ReadRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	page, err := reader.Read(ctx, "#ops", ids[0], 50)
	if err != nil || page.Gap || len(page.Messages) != 2 || page.Messages[0].ID != ids[1] || page.Messages[1].ID != ids[2] {
		t.Fatalf("%+v %v", page, err)
	}
	if page, err = reader.Read(ctx, "#ops", ids[2], 50); err != nil || len(page.Messages) != 0 || page.Gap {
		t.Fatalf("caught up: %+v %v", page, err)
	}
	page, err = reader.Read(ctx, "#ops", strings.Repeat("0", 32), 50)
	if err != nil || !page.Gap || len(page.Messages) == 0 || page.Messages[0].ID != ids[0] {
		t.Fatalf("expired start: %+v %v", page, err)
	}
}

// While native IRC is unreachable, check reads followed rooms over another
// carrier from the cursors native IRC left, says the inbox is waiting, and
// leaves cursors the next native check continues from.
func TestCheckContinuesRoomsOverAnotherCarrier(t *testing.T) {
	f := newFallbackFixture(t)
	addr := f.proxy.addr()
	mustCLI(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "before")
	if got := checkBodies(t, mustCLI(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--json")); len(got) != 1 || got[0] != "before" {
		t.Fatalf("native check: %v", got)
	}
	f.proxy.close()
	mustCLI(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "during") // over the gateway
	out, stderr, err := cli(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--json")
	if err != nil {
		t.Fatal(err, stderr)
	}
	if got := checkBodies(t, out); len(got) != 1 || got[0] != "during" {
		t.Fatalf("check over a carrier: %v\n%s", got, out)
	}
	if footer := checkFooter(t, out); len(footer.Warnings) != 1 || !strings.Contains(footer.Warnings[0], "direct messages") {
		t.Fatalf("footer %+v", footer)
	}
	// Nothing new: an empty check, and a wait that a message ends.
	if got := checkBodies(t, mustCLI(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--json")); len(got) != 0 {
		t.Fatalf("read again: %v", got)
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _, _ = cli(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "waited for")
	}()
	if got := checkBodies(t, mustCLI(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--wait", "8s", "--json")); len(got) != 1 || got[0] != "waited for" {
		t.Fatalf("wait over a carrier: %v", got)
	}
	// Native IRC is back: it continues from the same cursor.
	startNativeProxy(t, addr, f.daemon)
	mustCLI(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "after")
	if got := checkBodies(t, mustCLI(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--json")); len(got) != 1 || got[0] != "after" {
		t.Fatalf("native check after recovery: %v", got)
	}
}

// A blocking check whose native connection dies keeps waiting over another
// carrier.
func TestCheckWaitSurvivesNativeIRCDying(t *testing.T) {
	f := newFallbackFixture(t)
	addr := f.proxy.addr()
	mustCLI(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "seen")
	mustCLI(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--json")
	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, _, err := cli(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--wait", "15s", "--json")
		done <- outcome{out, err}
	}()
	time.Sleep(500 * time.Millisecond) // the check is waiting natively
	f.proxy.close()
	time.Sleep(500 * time.Millisecond)
	mustCLI(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "while down")
	select {
	case got := <-done:
		if got.err != nil || len(checkBodies(t, got.out)) != 1 || checkBodies(t, got.out)[0] != "while down" {
			t.Fatalf("%v\n%s", got.err, got.out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the check did not return")
	}
}

// A watcher whose native connection dies keeps following its rooms over
// another carrier, then returns to native IRC, showing every message once.
func TestWatchFollowsRoomsOverAnotherCarrierWhileNativeIsDown(t *testing.T) {
	f := newFallbackFixture(t)
	fastReconnect(t)
	addr := f.proxy.addr()
	send(t, addr, "writer", "#room", "msg-A") // also learns the carriers
	out, _ := startWatch(t, "--channel", "#room", "--color", "never", "--width", "100", "--backlog", "5", "--addr", addr)
	waitForOutput(t, out, "--- live ---")

	f.proxy.close()
	waitForOutput(t, out, "connection lost")
	mustCLI(t, addr, "send", "--nick", "writer", "--channel", "#room", "--message", "msg-B") // over the gateway
	waitForOutput(t, out, "following rooms over "+f.carrier("http"))
	waitForOutput(t, out, "msg-B")

	startNativeProxy(t, addr, f.daemon)
	waitForOutput(t, out, "reconnected")
	send(t, addr, "writer", "#room", "msg-C")
	waitForOutput(t, out, "msg-C")
	got := out.String()
	for _, body := range []string{"msg-A", "msg-B", "msg-C"} {
		if n := strings.Count(got, body); n != 1 {
			t.Errorf("%s shown %d times, want exactly once:\n%s", body, n, got)
		}
	}
}

// The full-screen UI keeps its rooms live over another carrier while native
// IRC is down, posts there, and returns to native IRC without duplicates.
func TestUIFollowsRoomsOverAnotherCarrierWhileNativeIsDown(t *testing.T) {
	f := newFallbackFixture(t)
	fastReconnect(t)
	addr := f.proxy.addr()
	send(t, addr, "planner", "#ops", "before")
	h := startUIBackend(t, addr)
	h.until("history", func() bool { return h.model.connected && strings.Contains(h.texts("#ops"), "before") })

	f.proxy.close()
	h.until("the outage", func() bool { return !h.model.connected })
	mustCLI(t, addr, "send", "--nick", "planner", "--channel", "#ops", "--message", "during") // over the gateway
	h.until("the room over another carrier", func() bool { return strings.Contains(h.texts("#ops"), "planner: during") })
	h.cmds <- uiCmd{kind: "send", target: "#ops", text: "typed while down", requestID: "ui-bridge-1"}
	h.until("the UI's own post over another carrier", func() bool { return strings.Contains(h.texts("#ops"), "me: typed while down") })

	startNativeProxy(t, addr, f.daemon)
	h.until("native IRC again", func() bool { return h.model.connected })
	send(t, addr, "planner", "#ops", "after")
	h.until("a native message", func() bool { return strings.Contains(h.texts("#ops"), "planner: after") })
	got := h.texts("#ops")
	for _, body := range []string{"before", "during", "typed while down", "after"} {
		if n := strings.Count(got, body); n != 1 {
			t.Errorf("%q shown %d times:\n%s", body, n, got)
		}
	}
	if got := historyOf(t, f.daemon, "#ops", "typed while down"); len(got) != 1 || got[0].From != "me" {
		t.Fatalf("posted %+v", got)
	}
}

// fastKeepAlive shortens the session keepalive so a silent link is noticed
// within a test's patience.
func fastKeepAlive(t *testing.T) {
	t.Helper()
	keepAlive, probe := sessionKeepAlive, sessionProbeTimeout
	sessionKeepAlive, sessionProbeTimeout = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { sessionKeepAlive, sessionProbeTimeout = keepAlive, probe })
}

// A native link that goes silent without closing is noticed by keepalive,
// and the watcher continues over another carrier.
func TestWatchContinuesWhenTheNativeLinkGoesSilent(t *testing.T) {
	f := newFallbackFixture(t)
	fastReconnect(t)
	fastKeepAlive(t)
	addr := f.proxy.addr()
	send(t, addr, "writer", "#room", "msg-A")
	out, _ := startWatch(t, "--channel", "#room", "--color", "never", "--width", "100", "--addr", addr)
	waitForOutput(t, out, "--- live ---")

	f.proxy.freeze()
	started := time.Now()
	waitForOutput(t, out, "connection lost (server stopped answering)")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("a silent link was noticed after %s", elapsed)
	}
	mustCLI(t, addr, "send", "--nick", "writer", "--channel", "#room", "--message", "msg-B")
	waitForOutput(t, out, "msg-B")
}

// More rooms than the gateway allows watches per author are followed over
// another carrier, by check --wait and by watch alike.
func TestManyRoomsContinueOverAnotherCarrier(t *testing.T) {
	f := newFallbackFixture(t)
	fastReconnect(t)
	poll := carrierPoll
	carrierPoll = 100 * time.Millisecond
	t.Cleanup(func() { carrierPoll = poll })
	addr := f.proxy.addr()
	rooms := []string{"#r1", "#r2", "#r3", "#r4"}
	for _, room := range rooms {
		send(t, addr, "writer", room, "seen in "+room)
	}
	mustCLI(t, addr, "check", "--nick", "bob", "--channel", strings.Join(rooms, ","), "--json")
	out, _ := startWatch(t, "--channel", strings.Join(rooms, ","), "--color", "never", "--width", "100", "--addr", addr)
	waitForOutput(t, out, "--- live ---")
	f.proxy.close()
	waitForOutput(t, out, "following rooms over")

	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _, _ = cli(t, addr, "send", "--nick", "writer", "--channel", "#r4", "--message", "late in #r4")
	}()
	got := checkBodies(t, mustCLI(t, addr, "check", "--nick", "bob", "--channel", strings.Join(rooms, ","), "--wait", "8s", "--json"))
	if len(got) != 1 || got[0] != "late in #r4" {
		t.Fatalf("check over a carrier: %v", got)
	}
	waitForOutput(t, out, "late in #r4")
	for _, room := range rooms[:3] {
		mustCLI(t, addr, "send", "--nick", "writer", "--channel", room, "--message", "more in "+room)
		waitForOutput(t, out, "more in "+room)
	}
}

// A blocking check that started over another carrier returns to native IRC
// once it registers again, so a direct message wakes it.
func TestCheckWaitReturnsToNativeIRC(t *testing.T) {
	f := newFallbackFixture(t)
	addr := f.proxy.addr()
	mustCLI(t, addr, "send", "--nick", "alice", "--channel", "#ops", "--message", "seen")
	mustCLI(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--json")
	f.proxy.close()
	type outcome struct {
		out, stderr string
		err         error
	}
	done := make(chan outcome, 1)
	go func() {
		out, stderr, err := cli(t, addr, "check", "--nick", "bob", "--channel", "#ops", "--wait", "15s", "--json")
		done <- outcome{out, stderr, err}
	}()
	time.Sleep(time.Second) // waiting over the gateway
	startNativeProxy(t, addr, f.daemon)
	time.Sleep(3 * time.Second) // redialled
	mustCLI(t, addr, "send", "--nick", "alice", "--to", "bob", "--message", "a direct message")
	select {
	case got := <-done:
		if got.err != nil || !slices.Contains(checkBodies(t, got.out), "a direct message") {
			t.Fatalf("%v %s\n%s", got.err, got.stderr, got.out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the check did not return")
	}
}
