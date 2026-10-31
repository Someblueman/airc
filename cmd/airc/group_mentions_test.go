package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A group tag reaches its members as a nickname tag would: the server
// resolves @humans against the directory's kinds when the message is
// posted, so check --mentions finds it, flags it mentioned and the digest
// counts it as addressed, with the CLI matching nothing itself.
func TestCheckMentionsAndDigestCountAGroupTag(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	mustCLI(t, address, "profile", "--nick", "sws", "--human")
	mustCLI(t, address, "profile", "--nick", "builder", "--agent")
	send(t, address, "writer", "#room", "plain chatter")
	humans := send(t, address, "writer", "#room", "@humans the deploy is broken")
	agents := send(t, address, "writer", "#room", "@agents pick this up")
	send(t, address, "writer", "#room", "```\n@humans in code are literal\n```")
	if fmt.Sprint(humans.GroupMentions) != "[sws]" || fmt.Sprint(agents.GroupMentions) != "[builder]" {
		t.Fatalf("the receipts resolved %v and %v", humans.GroupMentions, agents.GroupMentions)
	}

	// The digest, before anything is read, counts the group tag as addressed.
	var d digestSummary
	out := mustCLI(t, address, "digest", "--nick", "sws", "--channel", "room", "--json")
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("digest output %q: %v", out, err)
	}
	if d.Addressed != 1 || len(d.Rooms) == 0 || d.Rooms[0].Target != "#room" || d.Rooms[0].Mentions != 1 || len(d.Waiting) != 1 || d.Waiting[0].ID != humans.ID {
		t.Fatalf("digest = %+v", d)
	}

	got := checkMessages(t, mustCLI(t, address, "check", "--nick", "sws", "--mentions", "--json"))
	if len(got) != 1 || got[0].ID != humans.ID || !got[0].Mentioned || fmt.Sprint(got[0].GroupMentions) != "[sws]" {
		t.Fatalf("check --mentions for the human returned %#v", got)
	}
	got = checkMessages(t, mustCLI(t, address, "check", "--nick", "Builder", "--mentions", "--json"))
	if len(got) != 1 || got[0].ID != agents.ID || !got[0].Mentioned {
		t.Fatalf("check --mentions for the agent returned %#v", got)
	}
	// A guest has no kind, so neither group is theirs; nobody was in the
	// room, so @here would have reached nobody either.
	if got = checkMessages(t, mustCLI(t, address, "check", "--nick", "visitor", "--mentions", "--json")); len(got) != 0 {
		t.Fatalf("check --mentions for a guest returned %#v", got)
	}
	room := mustCLI(t, address, "check", "--nick", "sws", "--channel", "#room")
	if strings.Count(room, "(mentions you)") != 1 {
		t.Fatalf("the room check should flag the @humans message once:\n%s", room)
	}
}
