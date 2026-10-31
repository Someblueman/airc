package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// accountClient dials nick as a registered account, creating it, and joins
// rooms.
func accountClient(t *testing.T, address, nick string, rooms ...string) *irc.Client {
	t.Helper()
	c, err := irc.Dial(irc.Config{Nick: nick, Addr: address, IdentityToken: testAdminToken, CreateAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	for _, room := range rooms {
		if err := c.Join(room); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "handoffs"}); err != nil {
		t.Fatal(err)
	}
	return c
}

// adminClient dials nick as a guest holding the daemon's admin credential.
func adminClient(t *testing.T, address, nick string) *irc.Client {
	t.Helper()
	c := handoffClient(t, address, nick)
	if err := c.AuthenticateAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	return c
}

func orchestrateRequest(room string, orchestrator, goal *string) protocol.ChatRequest {
	return protocol.ChatRequest{Action: "room", Target: room, Seconds: -1, Limit: -1, Orchestrator: orchestrator, Goal: goal}
}

// roomEntry sends a room request from c and returns the room entry answered.
func roomEntry(t *testing.T, c *irc.Client, r protocol.ChatRequest) (irc.ChatEntry, error) {
	t.Helper()
	entries, _, err := handoffChat(t, c, r)
	if err != nil {
		return irc.ChatEntry{}, err
	}
	if len(entries) != 1 || entries[0].Action != "room" {
		t.Fatalf("room answered %+v", entries)
	}
	return entries[0], nil
}

// nextSignal waits for c to be sent a 780 signal of action.
func nextSignal(t *testing.T, c *irc.Client, action string) *irc.SignalEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			if s, ok := e.(*irc.SignalEvent); ok && s.Action == action {
				return s
			}
		case <-timeout:
			t.Fatalf("no %s signal", action)
		}
	}
}

// Only an admin sets a room's orchestrator and goal; the orchestrator is a
// registered nickname; turning the mode on, changing it and turning it off
// each post one @agents announcement and signal open clients; the charter
// CHECK entry carries both fields; and the snapshot keeps them across a
// restart, refusing one whose orchestrator has no account.
func TestOrchestratorModeIsSetByAnAdminAnnouncedAndKept(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	lead := accountClient(t, address, "Lead", "#work")
	if err := lead.UpdateProfile(map[string]string{"kind": "agent"}); err != nil {
		t.Fatal(err)
	}
	worker := profiled(t, address, "worker", "agent", "#work")
	person := profiled(t, address, "person", "human", "#work")
	nick, goal := "lead", "ship the parser fix by Friday"

	// A room operator and a guest are refused; so is an unregistered nickname.
	boss := adminClient(t, address, "boss")
	if err := boss.SetOperator("#work", "Lead", true); err != nil {
		t.Fatal(err)
	}
	if _, err := roomEntry(t, boss, protocol.ChatRequest{Action: "room", Target: "#work", Seconds: -1, Limit: -1}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*irc.Client{lead, person} {
		if _, err := roomEntry(t, c, orchestrateRequest("#work", &nick, &goal)); err == nil || !strings.Contains(err.Error(), "only an admin may set the orchestrator") {
			t.Fatalf("%s set the orchestrator: %v", c.Nick(), err)
		}
	}
	empty := ""
	if _, err := roomEntry(t, lead, orchestrateRequest("#work", nil, &empty)); err == nil || !strings.Contains(err.Error(), "only an admin may set the orchestrator") {
		t.Fatalf("an operator set the goal: %v", err)
	}
	stranger := "worker"
	if _, err := roomEntry(t, boss, orchestrateRequest("#work", &stranger, &goal)); err == nil || !strings.Contains(err.Error(), "the orchestrator must be a registered nickname; airc user create --nick worker") {
		t.Fatalf("a guest became the orchestrator: %v", err)
	}
	if _, err := roomEntry(t, boss, orchestrateRequest("#work", nil, &goal)); err == nil || !strings.Contains(err.Error(), "a goal goes with an orchestrator") {
		t.Fatalf("a goal without an orchestrator: %v", err)
	}
	long, multi := strings.Repeat("g", protocol.MaxGoalBytes+1), "one\ntwo"
	for _, bad := range []*string{&long, &multi} {
		if _, err := roomEntry(t, boss, orchestrateRequest("#work", &nick, bad)); err == nil || !strings.Contains(err.Error(), "a goal is one line of at most 400 bytes") {
			t.Fatalf("goal %q: %v", *bad, err)
		}
	}

	// The admin turns it on, in the account's spelling, without joining.
	entry, err := roomEntry(t, boss, orchestrateRequest("#work", &nick, &goal))
	if err != nil || entry.Orchestrator != "Lead" || entry.Goal != goal {
		t.Fatalf("set: %+v %v", entry, err)
	}
	want := "@agents Lead orchestrates #work: ship the parser fix by Friday. Take Lead's handoffs, post status at changes, finish with a note or a link."
	m := nextMessage(t, worker, "orchestrate")
	if m.Message != want || m.From != "boss" || m.Target != "#work" || !slices.Equal(m.GroupMentions, []string{"lead", "worker"}) {
		t.Fatalf("announcement %q from %s to %s reaching %v", m.Message, m.From, m.Target, m.GroupMentions)
	}
	if signal := nextSignal(t, person, "orchestrate"); signal.Orchestrator != "Lead" || signal.Goal != goal || signal.From != "boss" {
		t.Fatalf("signal %+v", signal)
	}
	if mentions := mentionsOf(s, m.ID); !slices.Contains(mentions, "worker") || slices.Contains(mentions, "person") {
		t.Fatalf("the announcement mentions %v", mentions)
	}

	// Anyone reads it, with the room action and in the charter entry.
	if entry, err := roomEntry(t, person, protocol.ChatRequest{Action: "room", Target: "#work", Seconds: -1, Limit: -1}); err != nil || entry.Orchestrator != "Lead" || entry.Goal != goal {
		t.Fatalf("read: %+v %v", entry, err)
	}
	charter := checkCharter(t, s, "#work")
	if charter.Orchestrator != "Lead" || charter.Goal != goal {
		t.Fatalf("charter entry %+v", charter)
	}

	// The same settings again post nothing; a new goal is announced.
	if _, err := roomEntry(t, boss, orchestrateRequest("#work", &nick, &goal)); err != nil {
		t.Fatal(err)
	}
	next := "ship the lexer too"
	if _, err := roomEntry(t, boss, orchestrateRequest("#work", nil, &next)); err != nil {
		t.Fatal(err)
	}
	if m := nextMessage(t, worker, "orchestrate"); !strings.Contains(m.Message, "orchestrates #work: ship the lexer too.") {
		t.Fatalf("a repeat or the change posted %q", m.Message)
	}

	// The snapshot keeps it across a restart.
	worker.Close()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var saved chatState
	data, err := os.ReadFile(filepath.Join(dir, "chat"))
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Rooms["#work"].Orchestrator != "Lead" || saved.Rooms["#work"].Goal != next {
		t.Fatalf("snapshot %v: %s", err, data)
	}
	address, s = collaborationServer(t, dir)
	if charter := checkCharter(t, s, "#work"); charter.Orchestrator != "Lead" || charter.Goal != next {
		t.Fatalf("after a restart %+v", charter)
	}

	// Off clears both and is announced.
	worker = profiled(t, address, "worker", "agent", "#work")
	boss = adminClient(t, address, "boss")
	if entry, err := roomEntry(t, boss, orchestrateRequest("#work", &empty, nil)); err != nil || entry.Orchestrator != "" || entry.Goal != "" {
		t.Fatalf("off: %+v %v", entry, err)
	}
	if m := nextMessage(t, worker, "orchestrate"); m.Message != "@agents #work is no longer orchestrated." {
		t.Fatalf("off posted %q", m.Message)
	}
	s.mu.Lock()
	_, kept := s.chat.Rooms["#work"]
	s.mu.Unlock()
	if kept {
		t.Fatal("a room with nothing set stayed in the snapshot")
	}

	// A snapshot naming an orchestrator without an account is refused, and
	// so is a goal without an orchestrator.
	for _, room := range []roomSettings{{Orchestrator: "nobody", Goal: "g"}, {Goal: "g"}, {Orchestrator: "Lead", Goal: "a\nb"}} {
		bad := newChatState()
		bad.Rooms["#work"] = room
		data, _ = json.Marshal(bad)
		path := filepath.Join(t.TempDir(), "chat")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		loader := New(Config{HistoryLimit: 64})
		if err := loader.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
			t.Fatal(err)
		}
		err := loader.RestoreChat(path)
		switch {
		case room.Orchestrator == "nobody" && (err == nil || !strings.Contains(err.Error(), "#work: the orchestrator must be a registered nickname; airc user create --nick nobody")):
			t.Fatalf("an unregistered orchestrator loaded: %v", err)
		case room.Orchestrator != "nobody" && (err == nil || !strings.Contains(err.Error(), "invalid room orchestrator")):
			t.Fatalf("%+v loaded: %v", room, err)
		}
	}
}

// Deleting the orchestrator's account ends the mode, so the snapshot never
// names an orchestrator a restart would refuse.
func TestDeletingTheOrchestratorsAccountEndsTheMode(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	accountClient(t, address, "lead").Close()
	boss := adminClient(t, address, "boss")
	nick, goal := "lead", "ship it"
	if _, err := roomEntry(t, boss, orchestrateRequest("#work", &nick, &goal)); err != nil {
		t.Fatal(err)
	}
	if err := boss.Moderate(irc.AdminRequest{Action: "account-delete", Nick: "lead"}); err != nil {
		t.Fatal(err)
	}
	if entry, err := roomEntry(t, boss, protocol.ChatRequest{Action: "room", Target: "#work", Seconds: -1, Limit: -1}); err != nil || entry.Orchestrator != "" || entry.Goal != "" {
		t.Fatalf("after the delete %+v %v", entry, err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	collaborationServer(t, dir)
}

// checkCharter runs a CHECK with headers for room and returns its charter
// entry.
func checkCharter(t *testing.T, s *Server, room string) protocol.CheckEntry {
	t.Helper()
	data, _ := json.Marshal(protocol.CheckRequest{MaxMessages: 1, Headers: true, Targets: []protocol.CheckTarget{{Target: room, After: "*", Limit: 1}}})
	actor := &session{client: Client{Nick: "reader"}, out: make(chan string, 256), done: make(chan struct{})}
	s.mu.Lock()
	s.checkLocked(actor, protocol.Command{Trailing: string(data)})
	s.mu.Unlock()
	for len(actor.out) > 0 {
		c, err := protocol.Parse(strings.TrimRight(<-actor.out, "\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		var e protocol.CheckEntry
		if c.Name == "784" && json.Unmarshal([]byte(c.Trailing), &e) == nil && e.Kind == "charter" {
			return e
		}
	}
	t.Fatal("no charter entry")
	return protocol.CheckEntry{}
}
