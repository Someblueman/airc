package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestModerationRestoreExpiryAndFailClosedWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	s := New(Config{})
	if err := s.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreModeration(path); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rules := map[string]protocol.ModerationRule{}
	for _, kind := range []string{"ban", "mute"} {
		rule := protocol.ModerationRule{Kind: kind, Nick: "bot", Scope: "*", SetBy: "operator", SetAt: now, Reason: "saved"}
		rules[ruleKey(kind, "bot", "*")] = rule
	}
	if err := s.saveModerationLocked(rules); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("rules permissions")
	}
	restored := New(Config{})
	if err := restored.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreModeration(path); err != nil {
		t.Fatal(err)
	}
	if _, banned := restored.restrictionLocked("ban", "BOT", "#room"); !banned {
		t.Fatal("ban lost on restart")
	}
	if _, muted := restored.restrictionLocked("mute", "bot", "other"); !muted {
		t.Fatal("mute lost on restart")
	}
	// Expiry does not need a timer, connection or file rewrite.
	rule := rules[ruleKey("ban", "bot", "*")]
	rule.ExpiresAt = now.Add(-time.Second)
	restored.moderation[ruleKey("ban", "bot", "*")] = rule
	if _, banned := restored.restrictionLocked("ban", "bot", "*"); banned {
		t.Fatal("expired ban enforced")
	}
	if len(sortedRules(restored.moderation, now)) != 1 {
		t.Fatal("expired rule listed")
	}
	// Exercise the actual command's write failure: no successful response,
	// no in-memory ban, and no disconnected target.
	restored.moderationAt = filepath.Join(t.TempDir(), "missing", "rules")
	connection, peer := net.Pipe()
	defer peer.Close()
	target := &session{client: Client{Nick: "newbot"}, conn: connection, done: make(chan struct{})}
	defer target.close()
	restored.clients["target"] = target
	actor := &session{client: Client{Nick: "operator"}, admin: true, out: make(chan string, 4), done: make(chan struct{})}
	request, _ := json.Marshal(protocol.AdminRequest{Action: "ban", Nick: "newbot"})
	restored.adminLocked(actor, protocol.Command{Trailing: string(request)})
	if line := <-actor.out; !containsNumeric(line, "437") {
		t.Fatal(line)
	}
	if _, banned := restored.restrictionLocked("ban", "newbot", "*"); banned {
		t.Fatal("failed write applied")
	}
	select {
	case <-target.done:
		t.Fatal("failed persistence disconnected target")
	default:
	}
}

func containsNumeric(line, code string) bool {
	command, _ := protocol.Parse(line)
	return command.Name == code
}

func TestModerationRejectsCorruptSnapshotsAndBounds(t *testing.T) {
	now := time.Now().UTC()
	valid := protocol.ModerationRule{Kind: "mute", Nick: "bot", Scope: "*", SetBy: "operator", SetAt: now}
	encoded, _ := json.Marshal([]protocol.ModerationRule{valid})
	for _, contents := range []string{"null", "{}", "[] []", `[{}]`, `[{"extra":1}]`, string(encoded[:len(encoded)-1]) + "," + string(encoded[1:])} {
		t.Run(fmt.Sprintf("case-%x", []byte(contents)[:min(8, len(contents))]), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules")
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			s := New(Config{})
			if err := s.EnableAdmin(testAdminToken); err != nil {
				t.Fatal(err)
			}
			if err := s.RestoreModeration(path); err == nil {
				t.Fatal("accepted malformed rules")
			}
		})
	}
	// The largest valid collection must be restorable even when JSON escapes
	// every reason byte. This also tests the enforced rule count.
	path := filepath.Join(t.TempDir(), "full.json")
	s := New(Config{})
	if err := s.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreModeration(path); err != nil {
		t.Fatal(err)
	}
	for i := range maxModerationRules {
		rule := valid
		rule.Nick, rule.Reason = fmt.Sprintf("bot%d", i), string(makeEscapedReason())
		s.moderation[ruleKey(rule.Kind, rule.Nick, rule.Scope)] = rule
	}
	if err := s.saveModerationLocked(s.moderation); err != nil {
		t.Fatal(err)
	}
	restored := New(Config{})
	if err := restored.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreModeration(path); err != nil {
		t.Fatal(err)
	}
	actor := &session{client: Client{Nick: "operator"}, admin: true, out: make(chan string, 4), done: make(chan struct{})}
	s.adminLocked(actor, protocol.Command{Trailing: `{"action":"ban","nick":"overflow"}`})
	if line := <-actor.out; !containsNumeric(line, "437") {
		t.Fatal(line)
	}
}

func makeEscapedReason() []byte {
	result := make([]byte, 400)
	for i := range result {
		result[i] = '<'
	}
	return result
}
