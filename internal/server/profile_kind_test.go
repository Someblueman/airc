package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// A profile's kind says whether a nickname is a human or an agent. It is
// kept with the profile, so a card that says only that persists and is
// never reclaimed as an unprofiled one.
func TestProfileKindIsSavedAndValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	srv := New(Config{})
	if err := srv.RestoreProfiles(path); err != nil {
		t.Fatal(err)
	}
	client := directorySession("sws")
	srv.profileLocked(client, protocol.Command{Trailing: `{"kind":"bogus"}`})
	if line := <-client.out; !strings.Contains(line, " 461 ") || !strings.Contains(line, "human or agent") {
		t.Fatalf("an invalid kind was not refused: %s", line)
	}
	srv.profileLocked(client, protocol.Command{Trailing: `{"kind":"human"}`})
	for range 2 { // the card and the end of the reply
		<-client.out
	}
	now := time.Now().UTC()
	if card := srv.directoryCard("sws", now); card.Kind != "human" {
		t.Fatalf("card %+v", card)
	}
	if !srv.roomForCardLocked("someone-else", now.Add(time.Hour)) || srv.directory["sws"].Kind != "human" {
		t.Fatal("a kind-only card was reclaimed")
	}
	restored := New(Config{})
	if err := restored.RestoreProfiles(path); err != nil {
		t.Fatal(err)
	}
	if card := restored.directoryCard("SWS", now); card.Kind != "human" || card.State != "unknown" {
		t.Fatalf("restored %+v", card)
	}
	// Clearing the kind leaves nothing to keep.
	restored.profileLocked(directorySession("sws"), protocol.Command{Trailing: `{"kind":""}`})
	if data, _ := os.ReadFile(path); string(data) != "{}\n" || len(restored.directory) != 0 {
		t.Fatalf("an empty kind was kept: %s", data)
	}
	// A saved kind that is neither is rejected on restore.
	data, _ := json.Marshal(map[string]protocol.AgentCard{"sws": {Nick: "sws", AgentProfile: protocol.AgentProfile{Kind: "robot"}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := New(Config{}).RestoreProfiles(path); err == nil {
		t.Fatal("an invalid saved kind was restored")
	}
}
