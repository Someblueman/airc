package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/admin"
)

func TestAdminInitNeverOverwritesOrDisplaysCredential(t *testing.T) {
	agentEnv(t)
	t.Setenv("AIRC_ADMIN_TOKEN_FILE", "")
	output := mustCLI(t, "unused", "admin", "init")
	path, err := defaultAdminTokenFile()
	if err != nil {
		t.Fatal(err)
	}
	token, err := admin.ReadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, token) {
		t.Fatal("init printed credential")
	}
	deniedCLI(t, "unused", "file exists", "admin", "init")
	got, _ := admin.ReadToken(path)
	if got != token {
		t.Fatal("init replaced credential")
	}
}

func TestAdminWrongCredentialAndUnconfiguredDaemon(t *testing.T) {
	address, path := adminCLISetup(t)
	other := filepath.Join(t.TempDir(), "other.token")
	if err := admin.CreateToken(other); err != nil {
		t.Fatal(err)
	}
	deniedCLI(t, address, "Invalid admin credential", "admin", "ban", "bot", "--token-file", other)
	send(t, address, "bot", "#room", "still allowed")
	if output := mustCLI(t, address, "admin", "list"); !strings.Contains(output, "No active") {
		t.Fatal(output)
	}
	plain := cliTestServer(t)
	deniedCLI(t, plain, "lacks ADMIN", "admin", "list")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	deniedCLI(t, address, "chmod 600", "admin", "list")
}

func TestAdminTimedBanAndMuteExpireAcrossConnections(t *testing.T) {
	address, _ := adminCLISetup(t)
	ban := adminResult(t, address, "ban", "bot", "--for", "1s")
	mute := adminResult(t, address, "mute", "noisy", "--for", "1s")
	deniedCLI(t, address, "banned", "send", "--nick", "bot", "--channel", "#room", "--message", "denied")
	deniedCLI(t, address, "Muted", "send", "--nick", "noisy", "--channel", "#room", "--message", "denied")
	if ban.Rule.ExpiresAt.After(mute.Rule.ExpiresAt) {
		t.Fatal("unexpected expiry order")
	}
	advanceServerClock(t, 2*time.Second)
	send(t, address, "noisy", "#room", "mute expired")
	send(t, address, "bot", "#room", "ban expired")
	if output := mustCLI(t, address, "admin", "list"); !strings.Contains(output, "No active") {
		t.Fatal(output)
	}
}
