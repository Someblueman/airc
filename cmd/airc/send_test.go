package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSendNeverCollidesWithALiveSessionOrAnnouncesPresence(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	live, err := irc.Dial(irc.Config{Nick: "carol", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := live.Join("#room"); err != nil {
		t.Fatal(err)
	}
	// Same nick as the live session, into a channel it is in.
	mustCLI(t, address, "send", "--nick", "carol", "--channel", "#room", "--message", "from a one-shot")
	for {
		event := <-live.Events()
		switch e := event.(type) {
		case *irc.MessageEvent:
			if e.Message == "from a one-shot" {
				return
			}
		case *irc.QuitEvent:
			t.Fatalf("one-shot send announced %#v", e)
		case *irc.PartEvent:
			t.Fatalf("one-shot send announced %#v", e)
		}
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
