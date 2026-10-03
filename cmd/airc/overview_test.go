package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnreadCountsWithoutMovingCursors(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	if out := mustCLI(t, address, "unread", "--nick", "me", "--channel", "room"); out != "" {
		t.Fatalf("an empty unread must stay quiet for hooks: %q", out)
	}
	send(t, address, "writer", "#room", "first")
	send(t, address, "writer", "#room", "@me please look")
	mustCLI(t, address, "send", "--nick", "writer", "--to", "me", "--message", "direct")
	for range 2 { // a second run proves nothing was marked read
		var summary unreadSummary
		if err := json.Unmarshal([]byte(mustCLI(t, address, "unread", "--nick", "me", "--channel", "room", "--json")), &summary); err != nil {
			t.Fatal(err)
		}
		if summary.Unread != 3 || summary.Mentions != 2 || summary.More || len(summary.Targets) != 2 {
			t.Fatalf("wrong summary: %+v", summary)
		}
		if summary.Targets[0].Target != "#room" || summary.Targets[0].Unread != 2 || summary.Targets[0].Mentions != 1 || summary.Targets[0].FirstID == "" || summary.Targets[1].Target != "inbox" {
			t.Fatalf("wrong targets: %+v", summary.Targets)
		}
	}
	line := mustCLI(t, address, "unread", "--nick", "me", "--channel", "room")
	if !strings.Contains(line, "3 unread for me (2 addressed to you): #room 2, inbox 1") || strings.Count(line, "\n") != 1 {
		t.Fatalf("human summary: %q", line)
	}
	if out := mustCLI(t, address, "unread", "--nick", "me", "--mentions", "--json"); !strings.Contains(out, `"unread":2`) {
		t.Fatalf("mentions-only count: %q", out)
	}
	if bodies := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json")); len(bodies) != 3 {
		t.Fatalf("check after unread lost messages: %v", bodies)
	}
}

func TestCheckFromNowSkipsBacklogButKeepsHeaders(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--set", "Rules of the room")
	send(t, address, "writer", "#room", "old chatter")
	mustCLI(t, address, "send", "--nick", "writer", "--to", "me", "--message", "old assignment")
	out := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--from-now", "--json")
	if !strings.Contains(out, "Rules of the room") || strings.Contains(out, "old chatter") || strings.Contains(out, "old assignment") {
		t.Fatalf("from-now output: %s", out)
	}
	if status := checkFooter(t, out); status.Code != "baseline_set" || status.Skipped != 2 || status.More {
		t.Fatalf("from-now status: %+v", status)
	}
	send(t, address, "writer", "#room", "fresh")
	if bodies := checkBodies(t, mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--json")); len(bodies) != 1 || bodies[0] != "fresh" {
		t.Fatalf("after baseline: %v", bodies)
	}
	if _, _, err := cli(t, address, "check", "--nick", "me", "--from-now", "--wait", "1s"); err == nil {
		t.Fatal("--from-now accepted --wait")
	}
}

func TestCompactCheckKeepsIDsAndDropsBookkeeping(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "hello")
	full := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--peek", "--json")
	compact := mustCLI(t, address, "check", "--nick", "me", "--channel", "room", "--compact", "--json")
	if !strings.Contains(full, `"seq"`) || !strings.Contains(full, `"request_id"`) {
		t.Fatalf("full output changed: %s", full)
	}
	var message checkMessage
	if err := json.Unmarshal([]byte(strings.SplitN(compact, "\n", 2)[0]), &message); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compact, `"seq"`) || strings.Contains(compact, `"request_id"`) || message.ID == "" || message.Message != "hello" || message.Timestamp.Nanosecond() != 0 {
		t.Fatalf("compact output: %s", compact)
	}
	if len(compact) >= len(full) {
		t.Fatalf("compact (%d bytes) is not smaller than full (%d)", len(compact), len(full))
	}
}

func TestChannelsListsRoomsWithRetainedHistory(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "hello")
	mustCLI(t, address, "topic", "#room", "--nick", "planner", "--set", "Header")
	out := mustCLI(t, address, "channels", "--json")
	if !strings.Contains(out, `"type":"channel"`) || !strings.Contains(out, `"name":"#room"`) || !strings.Contains(out, `"topic":"Header"`) {
		t.Fatalf("channels: %s", out)
	}
	if human := mustCLI(t, address, "channels"); !strings.HasPrefix(human, "#room\t") {
		t.Fatalf("channels: %q", human)
	}
}

func TestSharedNicknameWarnsOnceWhenTheUserChanges(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	check := func(session string) string {
		t.Setenv("AIRC_SESSION", session)
		send(t, address, "writer", "#room", "for "+session) // forces a cursor save
		_, stderr, err := cli(t, address, "check", "--nick", "shared", "--channel", "room")
		if err != nil {
			t.Fatal(err, stderr)
		}
		return stderr
	}
	if warning := check("agent-a"); warning != "" {
		t.Fatalf("first use warned: %q", warning)
	}
	if warning := check("agent-a"); warning != "" {
		t.Fatalf("same session warned: %q", warning)
	}
	if warning := check("agent-b"); !strings.Contains(warning, "another agent session (agent-a)") {
		t.Fatalf("second agent not warned: %q", warning)
	}
}
