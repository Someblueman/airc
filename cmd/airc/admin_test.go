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

// adminCLISetup starts a daemon with an admin credential and moderation rules,
// plus whatever else extra restores, and returns its address and token file.
func adminCLISetup(t *testing.T, extra func(*server.Server) error) (string, string) {
	t.Helper()
	agentEnv(t)
	t.Setenv("AIRC_IDENTITY_FILE", "")
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
		if err := s.RestoreModeration(path + ".rules"); err != nil {
			return err
		}
		if extra != nil {
			return extra(s)
		}
		return nil
	})
	return address, path
}

func deniedCLI(t *testing.T, address, want string, args ...string) {
	t.Helper()
	_, _, err := cli(t, address, args...)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected %q denial, got %v", want, err)
	}
}

func TestAdminMuteScopesAndAllPostingPaths(t *testing.T) {
	address, _ := adminCLISetup(t, nil)
	var root irc.SendReceiptEvent
	output := mustCLI(t, address, "send", "--nick", "alice", "--channel", "#room", "--message", "root", "--json")
	if err := json.Unmarshal([]byte(output), &root); err != nil {
		t.Fatal(err)
	}
	// Seed a reaction so its deduplicated receipt cannot bypass the mute.
	mustCLI(t, address, "react", root.ID, "seen", "--nick", "bot")
	result := cliJSON[irc.AdminEvent](t, address, "admin", "mute", "BOT", "--channel", "room", "--reason", "noise", "--for", "10m")
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
	cliJSON[irc.AdminEvent](t, address, "admin", "mute", "bot")
	for _, args := range [][]string{
		{"send", "--nick", "bot", "--channel", "#other", "--message", "denied"},
		{"send", "--nick", "bot", "--to", "alice", "--message", "denied DM"},
		{"profile", "--nick", "bot", "--about", "denied"},
		{"presence", "--nick", "bot", "--set", "thinking"},
	} {
		deniedCLI(t, address, "Muted", args...)
	}
	cliJSON[irc.AdminEvent](t, address, "admin", "unmute", "bot")
	deniedCLI(t, address, "Muted", "send", "--nick", "bot", "--channel", "#room", "--message", "still room muted")
	cliJSON[irc.AdminEvent](t, address, "admin", "unmute", "bot", "--channel", "#room")
	send(t, address, "bot", "#room", "restored")
	if result := cliJSON[irc.AdminEvent](t, address, "admin", "unmute", "bot"); result.Changed {
		t.Fatal("absent rule reported changed")
	}
}

func TestAdminKickBanAndRoomBan(t *testing.T) {
	address, _ := adminCLISetup(t, nil)
	var clients []*irc.Client
	for range 2 {
		client, err := irc.Dial(irc.Config{Nick: "bot", Addr: address, Ephemeral: true})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		clients = append(clients, client)
	}
	result := cliJSON[irc.AdminEvent](t, address, "admin", "kick", "BoT", "--reason", "reset")
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
	result = cliJSON[irc.AdminEvent](t, address, "admin", "ban", "bot", "--reason", "stop")
	deniedCLI(t, address, "banned", "send", "--nick", "BOT", "--channel", "#room", "--message", "denied")
	if output := mustCLI(t, address, "admin", "list"); !strings.Contains(output, "stop") {
		t.Fatal(output)
	}
	cliJSON[irc.AdminEvent](t, address, "admin", "unban", "bot")
	send(t, address, "bot", "#room", "unbanned")
	cliJSON[irc.AdminEvent](t, address, "admin", "ban", "bot", "--channel", "#room")
	deniedCLI(t, address, "Banned", "send", "--nick", "bot", "--channel", "#room", "--message", "denied")
	send(t, address, "bot", "#other", "allowed")
	mustCLI(t, address, "history", "#room", "--nick", "bot")
	cliJSON[irc.AdminEvent](t, address, "admin", "unban", "bot", "--channel", "#room")
	if output := mustCLI(t, address, "admin", "list"); !strings.Contains(output, "No active") {
		t.Fatal(output)
	}
	if result := cliJSON[irc.AdminEvent](t, address, "admin", "kick", "offline"); result.Kicked != 0 {
		t.Fatal(result)
	}
}
