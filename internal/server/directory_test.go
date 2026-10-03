package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func directorySession(nick string) *session {
	return &session{client: Client{Nick: nick}, out: make(chan string, 32), done: make(chan struct{})}
}

func TestPresenceExpiryDoesNotImplyAvailabilityOrExtendOnActivity(t *testing.T) {
	srv := New(Config{})
	client := directorySession("alice")
	srv.presenceLocked(client, protocol.Command{Params: []string{"thinking", "1"}, Trailing: "Checking the approach"})
	deadline := srv.directory["alice"].ExpiresAt
	srv.touchCardLocked("ALICE")
	if card := srv.directoryCard("ALICE", deadline.Add(-time.Nanosecond)); card.State != "thinking" || card.ExpiresAt != deadline || card.Connected {
		t.Fatalf("active presence: %+v", card)
	}
	if card := srv.directoryCard("alice", deadline); card.State != "unknown" || card.Note != "" || card.LastSeen.IsZero() {
		t.Fatalf("expired presence: %+v", card)
	}
	// A recorded old connection flag never makes a disconnected nickname live.
	card := srv.directory["alice"]
	card.Connected = true
	srv.directory["alice"] = card
	if srv.directoryCard("alice", deadline).Connected {
		t.Fatal("stale connection flag survived")
	}
}

func TestDirectoryIsBoundedAndReclaimsOnlyExpiredUnprofiledCards(t *testing.T) {
	srv := New(Config{})
	now := time.Now().UTC()
	for i := 0; i < maxDirectoryCards; i++ {
		name := fmt.Sprintf("agent%d", i)
		srv.directory[name] = protocol.AgentCard{Nick: name, AgentProfile: protocol.AgentProfile{About: "known agent"}, State: "unknown"}
	}
	if srv.roomForCardLocked("new-agent", now) {
		t.Fatal("directory exceeded its cap")
	}
	srv.directory["agent0"] = protocol.AgentCard{Nick: "agent0", State: "thinking", ExpiresAt: now.Add(time.Second)}
	if srv.roomForCardLocked("new-agent", now) {
		t.Fatal("live activity was discarded")
	}
	if !srv.roomForCardLocked("new-agent", now.Add(time.Second)) || len(srv.directory) != maxDirectoryCards-1 {
		t.Fatal("expired unprofiled entry was not reclaimed")
	}
	for i := 0; i < 2000; i++ {
		name := fmt.Sprintf("temporary%d", i)
		if !srv.roomForCardLocked(name, now) {
			t.Fatal("expired churn exhausted directory")
		}
		srv.directory[name] = protocol.AgentCard{Nick: name, ExpiresAt: now.Add(-time.Second)}
	}
	if len(srv.directory) > maxDirectoryCards || srv.directory["agent1"].About != "known agent" {
		t.Fatal("churn removed a profile or grew the directory")
	}
}

func TestProfilePersistencePreservesFieldsButResetsActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	srv := New(Config{})
	if err := srv.RestoreProfiles(path); err != nil {
		t.Fatal(err)
	}
	client := directorySession("alice")
	srv.profileLocked(client, protocol.Command{Trailing: `{"model":"reasoner","workspace":"/code","about":"reviewer"}`})
	srv.presenceLocked(client, protocol.Command{Params: []string{"thinking", "3600"}, Trailing: "working"})
	srv.profileLocked(client, protocol.Command{Trailing: `{"tools":"go"}`})
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("profile permissions: %v %v", info, err)
	}
	restored := New(Config{})
	if err := restored.RestoreProfiles(path); err != nil {
		t.Fatal(err)
	}
	card := restored.directoryCard("ALICE", time.Now())
	if card.Model != "reasoner" || card.Tools != "go" || card.About != "reviewer" || card.State != "unknown" || card.Connected || !card.ExpiresAt.IsZero() {
		t.Fatalf("restored profile: %+v", card)
	}
	restored.profileLocked(directorySession("alice"), protocol.Command{Trailing: `{"clear":"true"}`})
	data, _ := os.ReadFile(path)
	if string(data) != "{}\n" || len(restored.directory) != 0 {
		t.Fatalf("clear did not persist: %s", data)
	}
}

func TestFailedProfileSaveDoesNotClaimSuccessOrMutateProfile(t *testing.T) {
	srv := New(Config{})
	srv.directory["alice"] = protocol.AgentCard{Nick: "alice", AgentProfile: protocol.AgentProfile{About: "original"}}
	srv.profilesAt = filepath.Join(t.TempDir(), "missing", "profiles.json")
	client := directorySession("alice")
	srv.profileLocked(client, protocol.Command{Trailing: `{"about":"changed"}`})
	if srv.directory["alice"].About != "original" {
		t.Fatal("failed save changed the profile")
	}
	if line := <-client.out; !strings.Contains(line, " 437 ") || !strings.Contains(line, "not saved") {
		t.Fatalf("failed save receipt: %s", line)
	}
	// Restore rejects oversized/corrupt input rather than accepting arbitrary state.
	path := filepath.Join(t.TempDir(), "bad.json")
	data, _ := json.Marshal(map[string]protocol.AgentCard{"alice": {Nick: "alice", AgentProfile: protocol.AgentProfile{About: strings.Repeat("x", 401)}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := New(Config{}).RestoreProfiles(path); err == nil {
		t.Fatal("invalid profile restored")
	}
	if err := os.WriteFile(path, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	// An unreadable document is set aside so the daemon can still start.
	recovered := New(Config{})
	if err := recovered.RestoreProfiles(path); err != nil || recovered.directory == nil {
		t.Fatal("null profile document was fatal or restored as a nil map", err)
	}
	if kept, err := os.ReadFile(path + ".bad"); err != nil || string(kept) != "null" {
		t.Fatal("unreadable profiles were not kept for inspection", err)
	}
	topics := filepath.Join(t.TempDir(), "topics.json")
	if err := os.WriteFile(topics, nil, 0600); err != nil { // a truncated write
		t.Fatal(err)
	}
	if err := New(Config{}).RestoreTopics(topics); err != nil {
		t.Fatal("an empty topics file prevented startup", err)
	}
	if _, err := os.Stat(topics + ".bad"); err != nil {
		t.Fatal("unreadable topics were not kept for inspection", err)
	}
}
