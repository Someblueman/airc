package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

// airc wake registers, shows and clears an account's wake URL, making a
// secret it prints once or reading one from a file; delivery itself is
// tested in internal/server.
func TestWakeCommand(t *testing.T) {
	dir := t.TempDir()
	address, _ := adminCLISetup(t, func(s *server.Server) error {
		if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
			return err
		}
		return s.EnableWake([]string{"https://hooks.example/airc/"}, filepath.Join(dir, "wakes"), nil)
	})
	mustCLI(t, address, "user", "create", "--nick", "carol")

	if _, stderr, err := cli(t, address, "wake", "--nick", "carol", "--url", "https://elsewhere.example/x"); err == nil || !strings.Contains(err.Error()+stderr, "allowlist") {
		t.Fatalf("an unlisted URL: %v %s", err, stderr)
	}
	set := cliJSON[wakeResult](t, address, "wake", "--nick", "carol", "--url", "https://hooks.example/airc/carol", "--quiet", "5m")
	if !set.Registered || set.URL != "https://hooks.example/airc/carol" || set.QuietSeconds != 300 || len(set.Secret) != 64 || set.ExpiresAt == nil {
		t.Fatalf("registered %+v", set)
	}
	status := cliJSON[wakeResult](t, address, "wake", "--nick", "carol")
	if !status.Registered || status.Secret != "" || status.URL != set.URL {
		t.Fatalf("status %+v", status)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(strings.Repeat("ab", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if own := cliJSON[wakeResult](t, address, "wake", "--nick", "carol", "--url", "https://hooks.example/airc/carol2", "--secret-file", secret); !own.Registered || own.Secret != "" || own.QuietSeconds != 60 {
		t.Fatalf("own secret %+v", own)
	}
	if cleared := cliJSON[wakeResult](t, address, "wake", "--nick", "carol", "--clear"); cleared.Registered {
		t.Fatalf("cleared %+v", cleared)
	}
	for _, bad := range [][]string{{"--clear", "--url", "https://hooks.example/airc/x"}, {"--secret-file", secret}, {"--url", "https://hooks.example/airc/x", "--quiet", "1s"}, {"extra"}} {
		if _, _, err := cli(t, address, append([]string{"wake", "--nick", "carol"}, bad...)...); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	plain := cliTestServerSetup(t, server.Config{HistoryLimit: 8}, nil)
	if _, stderr, err := cli(t, plain, "wake", "--nick", "carol"); err == nil || !strings.Contains(err.Error()+stderr, "--wake-allow") {
		t.Fatalf("a daemon without wake notices: %v %s", err, stderr)
	}
}
