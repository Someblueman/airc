package main

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

// A status posts a shaped room message, the daemon keeps each nickname's
// latest for cards, agents and the room's listing, and the digest says where
// everyone is without waiting on any of it.
func TestStatusThroughTheCLI(t *testing.T) {
	address := chatServer(t, 64)
	deniedCLI(t, address, "--state is working, blocked or done", "status", "--nick", "builder", "--channel", "work", "--task", "port the fix", "--state", "idle")
	deniedCLI(t, address, "--link needs an absolute http(s) URL", "status", "--nick", "builder", "--channel", "work", "--task", "port the fix", "--state", "done", "--link", "pr/54")
	deniedCLI(t, address, "a status is for a room", "status", "--nick", "builder", "--task", "port the fix", "--state", "working")
	deniedCLI(t, address, "go with --task", "status", "--nick", "builder", "--channel", "work", "--state", "working")
	deniedCLI(t, address, "at most 200 bytes", "status", "--nick", "builder", "--channel", "work", "--task", strings.Repeat("x", 201), "--state", "working")

	out := mustCLI(t, address, "status", "--nick", "builder", "--channel", "work", "--task", "port the fix", "--state", "working")
	if out != "builder [working] port the fix in #work\n" {
		t.Fatalf("status printed %q", out)
	}
	posted := cliJSON[statusRow](t, address, "status", "--nick", "builder", "--channel", "work", "--task", "port the fix", "--state", "working", "--message", "@planner the branch is work-7")
	if posted.Type != "status" || posted.Nick != "builder" || posted.Target != "#work" || posted.Task != "port the fix" || posted.State != "working" || posted.ID == "" || posted.At.IsZero() || posted.Message != "Status [working] port the fix: @planner the branch is work-7" {
		t.Fatalf("posted %+v", posted)
	}
	// A reader that knows no statuses reads the body as it stands; the note
	// tags planner, who sees it as a mention, but a status waits on nobody.
	if out := mustCLI(t, address, "history", "#work", "--nick", "reader"); !strings.Contains(out, "Status [working] port the fix: @planner the branch is work-7") {
		t.Fatalf("history shows %s", out)
	}
	if messages := checkMessages(t, mustCLI(t, address, "check", "--nick", "planner", "--mentions", "--json")); len(messages) != 1 || messages[0].Kind != "status" || messages[0].Task != "port the fix" || !messages[0].Mentioned {
		t.Fatalf("planner's mentions %+v", messages)
	}

	// The daemon keeps the latest status per nickname.
	link := "https://github.com/o/r/pull/54"
	out = mustCLI(t, address, "status", "--nick", "builder", "--channel", "work", "--task", "port the fix", "--state", "done", "--link", link, "--message", "merged in #54")
	if out != "builder [done] port the fix in #work "+link+"\n  merged in #54\n" {
		t.Fatalf("done printed %q", out)
	}
	mustCLI(t, address, "status", "--nick", "reviewer", "--channel", "work", "--task", "review the parser", "--state", "blocked", "--message", "waiting on builder")
	if out := mustCLI(t, address, "status", "--channel", "work"); out != "reviewer [blocked] review the parser in #work\nbuilder [done] port the fix in #work "+link+"\n" {
		t.Fatalf("the room's statuses read %q", out)
	}
	listed := jsonLines[statusRow](t, mustCLI(t, address, "status", "--channel", "work", "--json"))
	if len(listed) != 2 || listed[0].Nick != "reviewer" || listed[0].State != "blocked" || listed[1].Nick != "builder" || listed[1].Link != link || listed[1].ID != "" {
		t.Fatalf("the room's statuses %+v", listed)
	}
	if out := mustCLI(t, address, "status", "--channel", "ops"); out != "No statuses in #ops.\n" {
		t.Fatalf("an empty room printed %q", out)
	}

	// Cards and agent rows carry it.
	card := cliJSON[irc.DirectoryEvent](t, address, "directory", "--who", "builder")
	if card.Status == nil || card.Status.State != "done" || card.Status.Task != "port the fix" || card.Status.Target != "#work" || card.Status.Link != link {
		t.Fatalf("builder's card %+v", card.Status)
	}
	if out := mustCLI(t, address, "directory", "--who", "reviewer"); !strings.Contains(out, "status: [blocked] review the parser in #work") {
		t.Fatalf("reviewer's card reads %q", out)
	}
	agents := cliJSON[[]irc.AgentInfo](t, address, "agents")
	var builder *irc.AgentInfo
	for i := range agents {
		if agents[i].Nick == "builder" {
			builder = &agents[i]
		}
	}
	if builder == nil || builder.Status == nil || builder.Status.State != "done" {
		t.Fatalf("agents lists %+v", agents)
	}
	if out := mustCLI(t, address, "agents"); !strings.Contains(out, "builder\t#work\tactive ") || !strings.Contains(out, "\t[done] port the fix\n") {
		t.Fatalf("agents reads %q", out)
	}

	// The digest says where everyone is, newest first, and waits on none of it.
	digest := cliJSON[digestSummary](t, address, "digest", "--nick", "planner", "--channel", "work")
	if len(digest.Waiting) != 0 || len(digest.Rooms) != 1 || len(digest.Rooms[0].Statuses) != 2 || digest.Rooms[0].Statuses[0].Nick != "reviewer" || digest.Rooms[0].Statuses[1].Link != link {
		t.Fatalf("planner's digest %+v", digest)
	}
	text := mustCLI(t, address, "digest", "--nick", "planner", "--channel", "work")
	if !strings.Contains(text, "(0 waiting on you)") || !strings.Contains(text, "Where everyone is:\n  #work reviewer [blocked] review the parser (") || !strings.Contains(text, "  #work builder [done] port the fix (") || !strings.Contains(text, ") "+link+"\n") {
		t.Fatalf("planner's digest reads %s", text)
	}
}
