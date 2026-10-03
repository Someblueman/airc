package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func adminAccountsSetup(t *testing.T) string {
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
	dir := t.TempDir()
	return cliTestServerSetup(t, server.Config{HistoryLimit: 64}, func(s *server.Server) error {
		if err := s.EnableAdmin(token); err != nil {
			return err
		}
		if err := s.RestoreModeration(path + ".rules"); err != nil {
			return err
		}
		return s.RestoreAccounts(filepath.Join(dir, "accounts"))
	})
}

func TestAdminAccountListAndDelete(t *testing.T) {
	address := adminAccountsSetup(t)
	if output := mustCLI(t, address, "admin", "account-list"); !strings.Contains(output, "No registered accounts") {
		t.Fatal(output)
	}
	mustCLI(t, address, "user", "create", "--nick", "alice")
	mustCLI(t, address, "user", "create", "--nick", "bob")
	output := mustCLI(t, address, "admin", "account-list")
	if lines := strings.Split(strings.TrimSpace(output), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "alice ") || !strings.HasPrefix(lines[1], "bob ") {
		t.Fatalf("account-list: %q", output)
	}
	if output := mustCLI(t, address, "admin", "account-list", "--json"); !strings.Contains(output, `"account_id"`) || !strings.Contains(output, `"action":"account-list"`) {
		t.Fatal(output)
	}
	if client, err := irc.Dial(irc.Config{Nick: "alice", Addr: address, Ephemeral: true}); err == nil {
		client.Close()
		t.Fatal("guest took a registered nickname")
	}

	if output := mustCLI(t, address, "admin", "account-delete", "ALICE"); !strings.Contains(output, "removed account") {
		t.Fatal(output)
	}
	if output := mustCLI(t, address, "admin", "account-delete", "alice"); !strings.Contains(output, "no such account") {
		t.Fatal(output)
	}
	if output := mustCLI(t, address, "admin", "account-delete", "bob", "--json"); !strings.Contains(output, `"changed":true`) {
		t.Fatal(output)
	}
	if output := mustCLI(t, address, "admin", "account-list"); !strings.Contains(output, "No registered accounts") {
		t.Fatal(output)
	}
	client, err := irc.Dial(irc.Config{Nick: "alice", Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatalf("freed nickname unavailable: %v", err)
	}
	client.Close()
}

func TestAdminAccountArgumentsAndAvailability(t *testing.T) {
	address := adminAccountsSetup(t)
	deniedCLI(t, address, "requires a nickname", "admin", "account-delete")
	deniedCLI(t, address, "unexpected admin arguments", "admin", "account-list", "alice")
	deniedCLI(t, address, "does not accept", "admin", "account-delete", "alice", "--reason", "x")
	deniedCLI(t, address, "does not accept", "admin", "account-delete", "alice", "--channel", "#room")
	deniedCLI(t, address, "does not accept", "admin", "account-list", "--for", "1m")

	// A daemon without an accounts file says so rather than reporting success.
	plain, _ := adminCLISetup(t)
	deniedCLI(t, plain, "Accounts are not enabled", "admin", "account-delete", "alice")
	deniedCLI(t, plain, "Accounts are not enabled", "admin", "account-list")
}
