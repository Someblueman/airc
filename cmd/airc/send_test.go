package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSendNeverTakesALiveNicknameOrAnnouncesPresence(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	assertOneShotSendsBesideLiveClient(t, address, address)
}

// The live client's commands reach the server late, as when its connection
// is starved under load. Its JOIN must still be in place before the one-shot
// send, or the send lands in the room before the member and is never seen.
func TestSendNeverTakesALiveNicknameWhenTheLiveJoinIsSlow(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	assertOneShotSendsBesideLiveClient(t, address, slowUpstream(t, address, 300*time.Millisecond))
}

// assertOneShotSendsBesideLiveClient holds the nickname carol live in #room
// (connecting through liveAddress) and checks that one-shot sends to address
// cannot take that nickname and announce no presence to the room.
func assertOneShotSendsBesideLiveClient(t *testing.T, address, liveAddress string) {
	t.Helper()
	live, err := irc.Dial(irc.Config{Nick: "carol", Addr: liveAddress})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	joinLive(t, live, "#room")
	// A guest cannot speak as the nickname a live client holds (IRC's 433).
	if _, _, err := cli(t, address, "send", "--nick", "carol", "--channel", "#room", "--message", "as carol"); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("one-shot guest on a live nickname: %v", err)
	}
	// Another one-shot sender, into a channel the live session is in.
	mustCLI(t, address, "send", "--nick", "carol-cli", "--channel", "#room", "--message", "from a one-shot")
	nextLiveEvent(t, live, "the one-shot message", func(event irc.Event) bool {
		switch e := event.(type) {
		case *irc.MessageEvent:
			return e.Message == "from a one-shot"
		case *irc.QuitEvent:
			t.Fatalf("one-shot send announced %#v", e)
		case *irc.PartEvent:
			t.Fatalf("one-shot send announced %#v", e)
		}
		return false
	})
}

// slowUpstream proxies connections to address, holding each chunk a client
// sends for delay before forwarding it; replies pass straight through.
func slowUpstream(t *testing.T, address string, delay time.Duration) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			down, err := listener.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", address)
			if err != nil {
				_ = down.Close()
				continue
			}
			mu.Lock()
			conns = append(conns, down, up)
			mu.Unlock()
			closeBoth := func() { _ = down.Close(); _ = up.Close() }
			wg.Go(func() {
				defer closeBoth()
				_, _ = io.Copy(down, up)
			})
			wg.Go(func() {
				defer closeBoth()
				buf := make([]byte, 4096)
				for {
					n, err := down.Read(buf)
					if n > 0 {
						time.Sleep(delay)
						if _, werr := up.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			})
		}
	})
	return listener.Addr().String()
}

// A live client whose connection ends must fail the wait, not hand the test
// an endless run of nil events from the closed stream.
func TestAwaitClientEventFailsWhenTheStreamCloses(t *testing.T) {
	address := cliTestServer(t)
	live, err := irc.Dial(irc.Config{Nick: "carol", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	_ = live.Close()
	ctx, cancel := context.WithTimeout(context.Background(), liveEventWait)
	defer cancel()
	if _, err := awaitClientEvent(ctx, live, func(irc.Event) bool { return false }); err == nil || ctx.Err() != nil {
		t.Fatalf("closed stream: err = %v, ctx = %v", err, ctx.Err())
	}
}

func TestSendReportsDeliveryTargetsAndQueuedDirectMessages(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	live, err := irc.Dial(irc.Config{Nick: "builder", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	type result struct {
		ID, Target, Message string
		Delivered           *bool
	}
	for _, tc := range []struct {
		args      []string
		target    string
		delivered *bool // nil: a channel post has no delivery state
	}{
		{[]string{"--nick", "writer", "--channel", "#cli", "--message", "hello channel"}, "#cli", nil},
		{[]string{"--nick", "planner", "--to", "builder", "--message", "hello live"}, "builder", new(true)},
		{[]string{"--nick", "planner", "--to", "offline", "--message", "task 7"}, "offline", new(false)},
	} {
		got := cliJSON[result](t, address, append([]string{"send"}, tc.args...)...)
		if got.ID == "" || got.Target != tc.target || got.Message != tc.args[len(tc.args)-1] || (got.Delivered == nil) != (tc.delivered == nil) || got.Delivered != nil && *got.Delivered != *tc.delivered {
			t.Fatalf("send %v = %+v", tc.args, got)
		}
	}
	if out := mustCLI(t, address, "send", "--nick", "planner", "--to", "offline", "--message", "task 8"); !strings.Contains(out, "queued") {
		t.Fatalf("human output should say the message was queued: %q", out)
	}
	// The recipient reads it with check, without ever having been connected.
	got := checkBodies(t, mustCLI(t, address, "check", "--nick", "offline", "--json"))
	if fmt.Sprint(got) != "[task 7 task 8]" {
		t.Fatalf("inbox = %v", got)
	}
}

func TestSendSurfacesServerErrorsImmediately(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	start := time.Now()
	_, _, err := cli(t, address, "send", "--nick", "a", "--channel", "#", "--message", "x")
	if err == nil || !strings.Contains(err.Error(), "no such channel") {
		t.Fatalf("invalid channel error = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("error took %s; it should not wait for the confirmation timeout", time.Since(start))
	}
	bare := cliTestServerWith(t, server.Config{})
	_, _, err = cli(t, bare, "send", "--nick", "a", "--to", "nobody", "--message", "x")
	if err == nil || !strings.Contains(err.Error(), "--history") {
		t.Fatalf("unknown nick without history error = %v", err)
	}
	if _, _, err := cli(t, address, "send", "--nick", "a", "--channel", "#c", "--message", " \n "); err == nil {
		t.Fatal("a blank message should be rejected")
	}
}

func TestMultilineMessagesViaArgumentAndStdin(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "send", "--nick", "writer", "--channel", "#room", "--message", "from arg\nsecond line")

	var stdout, stderr bytes.Buffer
	report := "Report\n\n- one\n- two\n"
	if err := run([]string{"send", "--nick", "writer", "--channel", "#room", "--message", "-", "--addr", address, "--json"}, strings.NewReader(report), &stdout, &stderr); err != nil {
		t.Fatalf("send from stdin: %v (%s)", err, stderr.String())
	}
	var sent irc.MessageEvent
	if err := json.Unmarshal(stdout.Bytes(), &sent); err != nil || sent.Message != "Report\n\n- one\n- two" {
		t.Fatalf("stdin send result = %q, %v", sent.Message, err)
	}

	out := mustCLI(t, address, "check", "--nick", "me", "--channel", "#room", "--json")
	var got []checkMessage
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		var m checkMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("one JSON object per message must survive embedded newlines: %q", line)
		}
		if m.Type == "message" {
			got = append(got, m)
		}
	}
	if len(got) != 2 || got[0].Message != "from arg\nsecond line" || got[1].Message != "Report\n\n- one\n- two" {
		t.Fatalf("check returned %#v", got)
	}
	human := mustCLI(t, address, "history", "#room")
	if !strings.Contains(human, "writer: from arg\n  second line\n") {
		t.Fatalf("continuation lines should be indented in human output:\n%s", human)
	}
}

func TestSendUsesEnvironmentDefaults(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	t.Setenv("AIRC_NICK", "envbot")
	t.Setenv("AIRC_CHANNEL", "#envroom")
	mustCLI(t, address, "send", "--message", "hello from env")
	out := mustCLI(t, address, "history", "#envroom", "--json")
	if !strings.Contains(out, `"from":"envbot"`) || !strings.Contains(out, "hello from env") {
		t.Fatalf("history = %s", out)
	}
	// --to must win over a default channel.
	mustCLI(t, address, "send", "--to", "someone", "--message", "direct")
}
