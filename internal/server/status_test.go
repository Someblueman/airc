package server

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// mustStatus sends a status request and returns the one status entry answered.
func mustStatus(t *testing.T, c *irc.Client, r protocol.ChatRequest) irc.ChatEntry {
	t.Helper()
	entries, _, err := handoffChat(t, c, r)
	if err != nil {
		t.Fatalf("%s: %v", r.Action, err)
	}
	if len(entries) != 1 || entries[0].Action != "status" {
		t.Fatalf("%s answered %+v", r.Action, entries)
	}
	return entries[0]
}

// A status is a shaped room message; the daemon keeps each nickname's latest
// on its card and in agent listings, where a done status clears after a day
// and a working one stands.
func TestStatusPostsAreShapedAndTheLatestIsKept(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, Now: clock}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	planner := handoffClient(t, address, "planner", "#work", "#ops")
	reviewer := handoffClient(t, address, "reviewer", "#work")

	for want, r := range map[string]protocol.ChatRequest{
		"a status is for a room":      {Action: "status", Target: "planner", Task: "port the fix", State: "working"},
		"names its task":              {Action: "status", Target: "#work", State: "working"},
		"at most 200 bytes":           {Action: "status", Target: "#work", Task: strings.Repeat("x", protocol.MaxTaskBytes+1), State: "working"},
		"working, blocked, done":      {Action: "status", Target: "#work", Task: "port the fix", State: "idle"},
		"absolute http(s) URL":        {Action: "status", Target: "#work", Task: "port the fix", State: "done", Link: "pr/54"},
		"a status is for a room (DM)": {Action: "statuses", Target: "builder"},
	} {
		if _, _, err := handoffChat(t, builder, r); err == nil || !strings.Contains(err.Error(), strings.TrimSuffix(want, " (DM)")) {
			t.Errorf("%+v was accepted or refused otherwise: %v", r, err)
		}
	}

	working := mustStatus(t, builder, protocol.ChatRequest{Action: "status", Target: "#work", Task: "port the fix", State: "working"})
	if working.From != "builder" || working.State != "working" || working.Text != "port the fix" || !working.At.Equal(clock()) || working.Message == nil {
		t.Fatalf("status %+v", working)
	}
	if m := working.Message; m.Kind != "status" || m.Task != "port the fix" || m.State != "working" || m.Message != "Status [working] port the fix" {
		t.Fatalf("the status message %+v", m)
	}
	if m := nextMessage(t, reviewer, "status"); m.Target != "#work" || m.State != "working" || m.Task != "port the fix" || m.Message != "Status [working] port the fix" {
		t.Fatalf("the room saw %+v", m)
	}
	if mentions := mentionsOf(srv, working.Message.ID); len(mentions) != 0 {
		t.Fatalf("a status tagged %v", mentions)
	}

	card := func(c *irc.Client, nick string) irc.AgentCard {
		t.Helper()
		cards := listing[*irc.DirectoryEvent, *irc.EndOfDirectoryEvent](t, c, "DIRECTORY "+nick)
		if len(cards) != 1 {
			t.Fatalf("DIRECTORY %s: %+v", nick, cards)
		}
		return cards[0].AgentCard
	}
	if s := card(planner, "builder").Status; s == nil || s.Task != "port the fix" || s.State != "working" || s.Target != "#work" || s.Link != "" || !s.At.Equal(clock()) {
		t.Fatalf("builder's card carries %+v", s)
	}
	agents := listing[*irc.AgentsEvent, *irc.EndOfAgentsEvent](t, planner, "AGENTS ACTIVE")
	i := slices.IndexFunc(agents, func(e *irc.AgentsEvent) bool { return e.Agent.Nick == "builder" })
	if i < 0 || agents[i].Agent.Status == nil || agents[i].Agent.Status.State != "working" {
		t.Fatalf("AGENTS ACTIVE lists %+v", agents)
	}

	// The latest status replaces the one before; a note follows the task.
	advance(time.Minute)
	link := "https://github.com/o/r/pull/54"
	done := mustStatus(t, builder, protocol.ChatRequest{Action: "status", Target: "#work", Task: "port the fix", State: "done", Link: link, Text: "merged in #54"})
	if done.State != "done" || done.Link != link || done.Message.Message != "Status [done] port the fix: merged in #54" || done.Message.Link != link {
		t.Fatalf("done %+v", done)
	}
	advance(time.Minute)
	mustStatus(t, planner, protocol.ChatRequest{Action: "status", Target: "#work", Task: "write the notes", State: "blocked", Text: "waiting on builder"})
	mustStatus(t, planner, protocol.ChatRequest{Action: "status", Target: "#ops", Task: "rotate the key", State: "working"})

	// A room's listing is its nicknames' latest statuses there, newest first.
	listed, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "statuses", Target: "#work"})
	if err != nil || len(listed) != 1 || listed[0].From != "builder" || listed[0].State != "done" || listed[0].Link != link {
		t.Fatalf("#work statuses %+v: %v", listed, err)
	}
	if ops, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "statuses", Target: "#ops"}); err != nil || len(ops) != 1 || ops[0].From != "planner" || ops[0].Text != "rotate the key" {
		t.Fatalf("#ops statuses %+v: %v", ops, err)
	}
	advance(time.Minute)
	mustStatus(t, reviewer, protocol.ChatRequest{Action: "status", Target: "#work", Task: "review the parser", State: "working"})
	listed, _, err = handoffChat(t, reviewer, protocol.ChatRequest{Action: "statuses", Target: "#work"})
	if err != nil || len(listed) != 2 || listed[0].From != "reviewer" || listed[1].From != "builder" {
		t.Fatalf("#work statuses %+v: %v", listed, err)
	}

	// A day on, the done status is gone and the working ones stand, though
	// their posters are long past the activity window.
	advance(doneStatusKeep)
	if s := card(planner, "builder").Status; s != nil {
		t.Fatalf("builder's done status is still shown a day later: %+v", s)
	}
	if s := card(builder, "reviewer").Status; s == nil || s.State != "working" {
		t.Fatalf("reviewer's working status is gone: %+v", s)
	}
	if listed, _, err = handoffChat(t, reviewer, protocol.ChatRequest{Action: "statuses", Target: "#work"}); err != nil || len(listed) != 1 || listed[0].From != "reviewer" {
		t.Fatalf("#work statuses a day later %+v: %v", listed, err)
	}
}

// Pruning the activity map keeps a record whose status still stands past the
// activity window, and drops a done status once it is a day old.
func TestActivityPruneKeepsStandingStatuses(t *testing.T) {
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	s := New(Config{HistoryLimit: 8, Now: func() time.Time { return now }})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noteStatusLocked("worker", protocol.AgentStatus{Task: "port the fix", State: "working", Target: "#work", At: now})
	s.noteStatusLocked("finisher", protocol.AgentStatus{Task: "write the notes", State: "done", Target: "#work", At: now})
	s.noteActivityLocked("checker", "#work")
	s.pruneActivityLocked(now.Add(activeWindow))
	if len(s.activity) != 2 || s.activity["checker"] != nil || s.activity["finisher"].status == nil {
		t.Fatalf("after the window: %d records, checker %v", len(s.activity), s.activity["checker"])
	}
	s.pruneActivityLocked(now.Add(doneStatusKeep))
	if len(s.activity) != 1 || s.activity["worker"] == nil || s.activity["worker"].status.State != "working" {
		t.Fatalf("after a day: %d records", len(s.activity))
	}
	// A one-shot poster outside the window is still listed while its status
	// stands, and not once the status is gone.
	if s.agentStatusLocked("worker", now.Add(48*time.Hour)) == nil || s.agentStatusLocked("finisher", now) != nil {
		t.Fatal("status lookups disagree with the prune")
	}
}
