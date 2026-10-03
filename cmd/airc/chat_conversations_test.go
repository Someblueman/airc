package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestFollowedThreadsJoinOrdinaryChecksWithoutDuplicates(t *testing.T) {
	address := chatServer(t, 32)
	root := posted(t, address, "alice", "#other", "question")
	mustCLI(t, address, "follow", root.ID, "--nick", "reader")
	mustCLI(t, address, "check", "--nick", "reader", "--channel", "home", "--json")
	mustCLI(t, address, "send", "--nick", "bob", "--reply-to", root.ID, "--message", "followed answer")
	out := mustCLI(t, address, "check", "--nick", "reader", "--channel", "home", "--json")
	if strings.Count(out, "followed answer") != 1 {
		t.Fatal(out)
	}
	mustCLI(t, address, "unfollow", root.ID, "--nick", "reader")
	if following := mustCLI(t, address, "following", "--nick", "reader"); following != "" {
		t.Fatal(following)
	}
}

func TestReplyComingSignalsExpireAndAreNotAnswers(t *testing.T) {
	address := chatServer(t, 32)
	root := posted(t, address, "asker", "#room", "question")
	mustCLI(t, address, "prepare", root.ID, "--nick", "strong", "--eta", "1s", "--message", "considering")
	if out := mustCLI(t, address, "waiting", root.ID, "--json"); !strings.Contains(out, "considering") {
		t.Fatal(out)
	}
	if out := mustCLI(t, address, "check", "--nick", "asker", "--reply-to", root.ID, "--wait", "50ms", "--json"); len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != "wait_expired" {
		t.Fatal("signal became answer", out)
	}
	advanceServerClock(t, 2*time.Second)
	if out := mustCLI(t, address, "waiting", root.ID, "--json"); out != "" {
		t.Fatal("the reply promise did not expire", out)
	}
	mustCLI(t, address, "prepare", root.ID, "--nick", "strong")
	mustCLI(t, address, "prepare", root.ID, "--nick", "strong", "--cancel")
	if out := mustCLI(t, address, "waiting", root.ID, "--json"); out != "" {
		t.Fatal(out)
	}
	deniedCLI(t, address, "negative", "prepare", root.ID, "--nick", "strong", "--eta", "-1m")
	deniedCLI(t, address, "TTL", "typing", "--nick", "strong", "--channel", "room", "--for", "120s")
}

func TestExpiredFollowDoesNotBreakBlockingRoomChecks(t *testing.T) {
	address := chatServer(t, 2)
	root := posted(t, address, "alice", "#room", "old question")
	mustCLI(t, address, "follow", root.ID, "--nick", "reader")
	posted(t, address, "writer", "#room", "next")
	posted(t, address, "writer", "#room", "latest")
	out := mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--wait", "20ms", "--json")
	if !strings.Contains(out, "expired") || !strings.Contains(out, "latest") {
		t.Fatal(out)
	}
}

func TestPollsCustomReactionsAndActions(t *testing.T) {
	address := chatServer(t, 32)
	var result irc.ChatEntry
	output := mustCLI(t, address, "poll", "--nick", "host", "--channel", "room", "--question", "Which approach?", "--option", "simple", "--option", "fancy", "--for", "10m", "--json")
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, address, "user", "create", "--nick", "voter")
	mustCLI(t, address, "vote", result.ID, "1", "--nick", "voter")
	mustCLI(t, address, "vote", result.ID, "2", "--nick", "voter")
	var tally irc.ChatEntry
	if err := json.Unmarshal([]byte(mustCLI(t, address, "poll-results", result.ID, "--json")), &tally); err != nil {
		t.Fatal(err)
	}
	if len(tally.Votes) != 2 || tally.Votes[0] != 0 || tally.Votes[1] != 1 {
		t.Fatal(tally)
	}
	deniedCLI(t, address, "only the author", "poll-close", result.ID, "--nick", "voter")
	mustCLI(t, address, "react", result.ID, "🎉", "--nick", "voter")
	mustCLI(t, address, "me", "--nick", "voter", "--channel", "room", "--message", "waves")
	mustCLI(t, address, "poll-close", result.ID, "--nick", "host")
	deniedCLI(t, address, "poll is closed", "vote", result.ID, "1", "--nick", "voter")
}
