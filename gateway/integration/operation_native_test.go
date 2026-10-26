//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// Native probes and seeds shared by the operation tests. Anonymous probes use
// a unique nickname, so a probe never races the previous probe's disconnect.

func nativeCard(t *testing.T, socket, nick string) irc.AgentCard {
	t.Helper()
	client := dial(t, socket, fmt.Sprintf("card-reader-%d", roomReaderSequence.Add(1)), "", false, false)
	defer client.Close()
	if err := client.Directory(nick); err != nil {
		t.Fatal(err)
	}
	event := next(t, client, func(e *irc.DirectoryEvent) bool { return e.Nick == nick })
	next(t, client, func(e *irc.EndOfDirectoryEvent) bool { return true })
	return event.AgentCard
}

func nativePinMutation(t *testing.T, socket, target, id, action string) {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: action, Target: target, ID: id}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == action && e.ID == id })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
}

func pollNativeVote(t *testing.T, socket, pollID string, choice int) irc.ChatEntry {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "vote", ID: pollID, Choice: choice}); err != nil {
		t.Fatal(err)
	}
	entry := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "results" && e.ID == pollID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	return entry.ChatEntry
}

// roomReaderSequence names native probes uniquely.
var roomReaderSequence atomic.Uint64

func queryNativeRoom(t *testing.T, socket, target string) core.RoomResult {
	t.Helper()
	c := dial(t, socket, "room-reader-"+fmt.Sprint(roomReaderSequence.Add(1)), "", false, false)
	defer c.Close()
	if err := c.Chat(irc.ChatRequest{Action: "room", Target: target, Seconds: -1, Limit: -1}); err != nil {
		t.Fatal(err)
	}
	event := next(t, c, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.Target == target })
	next(t, c, func(e *irc.EndOfChatEvent) bool { return true })
	return core.RoomResult{Target: core.Target{Kind: "room", Name: target}, SlowSeconds: event.SlowSeconds, HistoryLimit: event.HistoryLimit}
}

func promoteRoomOperator(t *testing.T, socket string) {
	t.Helper()
	admin := dial(t, socket, "fixture-control", "", false, true)
	defer admin.Close()
	token, err := auth.ReadToken(filepath.Join(filepath.Dir(socket), "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateAdmin(token); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "381" })
	if err := admin.Raw("MODE #gateway-fixture +o fixture-author"); err != nil {
		t.Fatal(err)
	}
	if err := admin.Raw("MODE #gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	seen324, seen783 := false, false
	for !seen324 || !seen783 {
		event := next(t, admin, func(e *irc.RawEvent) bool { return e.Command == "324" || e.Command == "783" })
		if event.Command == "324" {
			seen324 = true
		} else {
			if len(event.Params) != 4 || event.Params[1] != "#gateway-fixture" || event.Params[2] != "fixture-author" || len(event.Params[3]) != 32 {
				t.Fatalf("foreign operator evidence: %+v", event)
			}
			seen783 = true
		}
	}
}
