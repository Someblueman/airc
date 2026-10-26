package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/server"
)

// --mentions-only and --quiet-hours travel on WAKE SET and come back in the
// status; a bad window is refused before connecting.
func TestWakeCommandOptions(t *testing.T) {
	dir := t.TempDir()
	address, _ := adminCLISetup(t, func(s *server.Server) error {
		if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
			return err
		}
		return s.EnableWake([]string{"https://ntfy.sh/"}, filepath.Join(dir, "wakes"), nil)
	})
	mustCLI(t, address, "user", "create", "--nick", "sws")
	set := cliJSON[wakeResult](t, address, "wake", "--nick", "sws", "--url", "https://ntfy.sh/sws-airc", "--mentions-only", "--quiet-hours", "22:00-08:00")
	if !set.Registered || !set.MentionsOnly || set.QuietHours != "22:00-08:00" || set.QuietSeconds != 60 {
		t.Fatalf("registered %+v", set)
	}
	status := mustCLI(t, address, "wake", "--nick", "sws")
	if !strings.Contains(status, "only for direct messages, messages that tag you") || !strings.Contains(status, "held during quiet hours 22:00-08:00") {
		t.Fatalf("status:\n%s", status)
	}
	plain := cliJSON[wakeResult](t, address, "wake", "--nick", "sws", "--url", "https://ntfy.sh/sws-airc")
	if plain.MentionsOnly || plain.QuietHours != "" {
		t.Fatalf("registering again without the options kept them: %+v", plain)
	}
	for _, bad := range [][]string{{"--url", "https://ntfy.sh/x", "--quiet-hours", "22:00"}, {"--url", "https://ntfy.sh/x", "--quiet-hours", "09:00-09:00"}, {"--mentions-only"}, {"--quiet-hours", "22:00-08:00"}} {
		if _, _, err := cli(t, "127.0.0.1:1", append([]string{"wake", "--nick", "sws"}, bad...)...); err == nil || strings.Contains(err.Error(), "dial") {
			t.Errorf("accepted %v: %v", bad, err)
		}
	}
}
