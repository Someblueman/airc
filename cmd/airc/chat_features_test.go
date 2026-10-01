package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func chatServer(t *testing.T, limit int) string {
	t.Helper()
	agentEnv(t)
	t.Setenv("AIRC_IDENTITY_FILE", "")
	dir := t.TempDir()
	return cliTestServerSetup(t, server.Config{HistoryLimit: limit}, func(s *server.Server) error {
		if err := s.EnableAdmin(strings.Repeat("a", 64)); err != nil {
			return err
		}
		if err := s.RestoreAccounts(filepath.Join(dir, "users")); err != nil {
			return err
		}
		if err := s.RestoreProfiles(filepath.Join(dir, "profiles")); err != nil {
			return err
		}
		return s.RestoreChat(filepath.Join(dir, "chat"))
	})
}

func posted(t *testing.T, address, nick, room, body string) irc.MessageEvent {
	t.Helper()
	var m irc.MessageEvent
	if err := json.Unmarshal([]byte(mustCLI(t, address, "send", "--nick", nick, "--channel", room, "--message", body, "--json")), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRegisteredUsersReconnectAndProtectProfiles(t *testing.T) {
	address := chatServer(t, 32)
	created := mustCLI(t, address, "user", "create", "--nick", "claude-reviewer", "--model", "Claude", "--about", "reviewer", "--json")
	if !json.Valid([]byte(created)) || !strings.Contains(created, `"ready":true`) {
		t.Fatal(created)
	}
	one := posted(t, address, "claude-reviewer", "#room", "first")
	two := posted(t, address, "CLAUDE-REVIEWER", "#room", "second")
	if one.AccountID == "" || two.AccountID != one.AccountID {
		t.Fatal("identity changed across connections")
	}
	output := mustCLI(t, address, "directory", "--who", "claude-reviewer", "--json")
	if !strings.Contains(output, `"about":"reviewer"`) || !strings.Contains(output, one.AccountID) {
		t.Fatal(output)
	}
	if client, err := irc.Dial(irc.Config{Nick: "claude-reviewer", Addr: address, Ephemeral: true}); err == nil {
		client.Close()
		t.Fatal("impersonated registered nickname")
	}
	mustCLI(t, address, "user", "create", "--nick", "claude-reviewer")
	path, err := identityPath(options{nick: "claude-reviewer", addr: address})
	if err != nil {
		t.Fatal(err)
	}
	value, err := loadIdentity(path, options{nick: "claude-reviewer", addr: address})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, value.Token) {
		t.Fatal("directory leaked credential")
	}
	if _, err := loadIdentity(path, options{nick: "claude-reviewer", addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("accepted another server's identity")
	}
}

func TestPinsCorrectionsRetractionsAndAuthorChecks(t *testing.T) {
	address := chatServer(t, 32)
	mustCLI(t, address, "user", "create", "--nick", "author")
	m := posted(t, address, "author", "#room", "original claim")
	mustCLI(t, address, "pin", m.ID, "--nick", "author")
	first := mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	if !strings.Contains(first, `"type":"pin"`) {
		t.Fatal(first)
	}
	if second := mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json"); strings.Contains(second, `"type":"pin"`) {
		t.Fatal("repeated unchanged pin")
	}
	deniedCLI(t, address, "only the author", "correct", m.ID, "--nick", "intruder", "--message", "lie")
	mustCLI(t, address, "correct", m.ID, "--nick", "author", "--message", "corrected claim")
	changed := mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	if !strings.Contains(changed, "superseded") || !strings.Contains(changed, "corrected claim") {
		t.Fatal(changed)
	}
	mustCLI(t, address, "retract", m.ID, "--nick", "author", "--message", "withdrawn")
	if history := mustCLI(t, address, "history", "#room", "--json"); !strings.Contains(history, `"retracted":true`) || !strings.Contains(history, "original claim") {
		t.Fatal(history)
	}
	mustCLI(t, address, "unpin", m.ID, "--nick", "author")
	if pins := mustCLI(t, address, "pins", "room", "--json"); pins != "" {
		t.Fatal(pins)
	}
	mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json")
	mustCLI(t, address, "pin", m.ID, "--nick", "author")
	if out := mustCLI(t, address, "check", "--nick", "reader", "--channel", "room", "--json"); !strings.Contains(out, `"type":"pin"`) {
		t.Fatal("re-added pin not surfaced", out)
	}
}

func TestRequestIDsAreStableAndConflictsFail(t *testing.T) {
	address := chatServer(t, 16)
	args := []string{"send", "--nick", "bot", "--channel", "room", "--message", "one\ntwo", "--request-id", "attempt-123", "--json"}
	var first, second irc.MessageEvent
	if err := json.Unmarshal([]byte(mustCLI(t, address, args...)), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(mustCLI(t, address, args...)), &second); err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.RequestID != "attempt-123" {
		t.Fatal("retry reposted")
	}
	deniedCLI(t, address, "different content", "send", "--nick", "bot", "--channel", "room", "--message", "different", "--request-id", "attempt-123")
	if history := mustCLI(t, address, "history", "room", "--json"); len(strings.Split(strings.TrimSpace(history), "\n")) != 1 {
		t.Fatal(history)
	}
}
