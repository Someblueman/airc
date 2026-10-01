package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func adminCLISetup(t *testing.T) (string, string) {
	t.Helper()
	agentEnv(t)
	path := filepath.Join(t.TempDir(), "admin.token")
	if err := admin.CreateToken(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIRC_ADMIN_TOKEN_FILE", path)
	token, err := admin.ReadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	address := cliTestServerSetup(t, server.Config{HistoryLimit: 64}, func(s *server.Server) error {
		if err := s.EnableAdmin(token); err != nil {
			return err
		}
		return s.RestoreModeration(path + ".rules")
	})
	return address, path
}

func adminResult(t *testing.T, address string, args ...string) irc.AdminEvent {
	t.Helper()
	output := mustCLI(t, address, append(append([]string{"admin"}, args...), "--json")...)
	var result irc.AdminEvent
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("result %s: %v", output, err)
	}
	return result
}

func deniedCLI(t *testing.T, address, want string, args ...string) {
	t.Helper()
	_, _, err := cli(t, address, args...)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected %q denial, got %v", want, err)
	}
}

func TestAdminMuteScopesAndAllPostingPaths(t *testing.T) {
	address, _ := adminCLISetup(t)
	var root irc.SendReceiptEvent
	output := mustCLI(t, address, "send", "--nick", "alice", "--channel", "#room", "--message", "root", "--json")
	if err := json.Unmarshal([]byte(output), &root); err != nil {
		t.Fatal(err)
	}
	// Seed a reaction so its deduplicated receipt cannot bypass the mute.
	mustCLI(t, address, "react", root.ID, "seen", "--nick", "bot")
	result := adminResult(t, address, "mute", "BOT", "--channel", "room", "--reason", "noise", "--for", "10m")
	if result.Rule.Scope != "#room" || result.Rule.ExpiresAt.IsZero() {
		t.Fatalf("rule: %+v", result)
	}
	for _, args := range [][]string{
		{"send", "--nick", "bot", "--channel", "#room", "--message", "denied"},
		{"send", "--nick", "bot", "--reply-to", root.ID, "--message", "denied reply"},
		{"react", root.ID, "seen", "--nick", "bot"},
		{"topic", "#room", "--nick", "bot", "--set", "denied topic"},
	} {
		deniedCLI(t, address, "Muted", args...)
	}
	send(t, address, "bot", "#other", "allowed")
	mustCLI(t, address, "send", "--nick", "bot", "--to", "alice", "--message", "allowed DM")
	mustCLI(t, address, "history", "#room", "--nick", "bot")
	adminResult(t, address, "mute", "bot")
	for _, args := range [][]string{
		{"send", "--nick", "bot", "--channel", "#other", "--message", "denied"},
		{"send", "--nick", "bot", "--to", "alice", "--message", "denied DM"},
		{"profile", "--nick", "bot", "--about", "denied"},
		{"presence", "--nick", "bot", "--set", "thinking"},
	} {
		deniedCLI(t, address, "Muted", args...)
	}
	adminResult(t, address, "unmute", "bot")
	deniedCLI(t, address, "Muted", "send", "--nick", "bot", "--channel", "#room", "--message", "still room muted")
	adminResult(t, address, "unmute", "bot", "--channel", "#room")
	send(t, address, "bot", "#room", "restored")
	if result := adminResult(t, address, "unmute", "bot"); result.Changed {
		t.Fatal("absent rule reported changed")
	}
}

func TestAdminKickBanAndRoomBan(t *testing.T) {
	address, _ := adminCLISetup(t)
	var clients []*irc.Client
	for i := 0; i < 2; i++ {
		client, err := irc.Dial(irc.Config{Nick: "bot", Addr: address, Ephemeral: true})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		clients = append(clients, client)
	}
	result := adminResult(t, address, "kick", "BoT", "--reason", "reset")
	if result.Kicked != 2 {
		t.Fatalf("kick count: %+v", result)
	}
	for _, client := range clients {
		closed := false
		timer := time.NewTimer(time.Second)
		for !closed {
			select {
			case _, ok := <-client.Events():
				closed = !ok
			case <-timer.C:
				t.Fatal("kicked session remained open")
			}
		}
		timer.Stop()
	}
	send(t, address, "bot", "#room", "reconnected")
	result = adminResult(t, address, "ban", "bot", "--reason", "stop")
	deniedCLI(t, address, "banned", "send", "--nick", "BOT", "--channel", "#room", "--message", "denied")
	if output := mustCLI(t, address, "admin", "list"); !strings.Contains(output, "stop") {
		t.Fatal(output)
	}
	adminResult(t, address, "unban", "bot")
	send(t, address, "bot", "#room", "unbanned")
	adminResult(t, address, "ban", "bot", "--channel", "#room")
	deniedCLI(t, address, "Banned", "send", "--nick", "bot", "--channel", "#room", "--message", "denied")
	send(t, address, "bot", "#other", "allowed")
	mustCLI(t, address, "history", "#room", "--nick", "bot")
	adminResult(t, address, "unban", "bot", "--channel", "#room")
	if output := mustCLI(t, address, "admin", "list"); !strings.Contains(output, "No active") {
		t.Fatal(output)
	}
	if result := adminResult(t, address, "kick", "offline"); result.Kicked != 0 {
		t.Fatal(result)
	}
}
