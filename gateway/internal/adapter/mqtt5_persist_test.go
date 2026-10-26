package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
)

func TestMQTT5SnapshotRoundTripAndRefusals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mqtt-x.sessions")
	s := newMQTT5Store(context.Background())
	s.path = path
	kept, _, _ := s.take("kept", "guest:alice", true, time.Hour)
	kept.subs["airc/room/a"] = &mqtt5Sub{filter: "airc/room/a", kind: mqtt5Room, target: "#a", qos: 1, after: "0123456789abcdef0123456789abcdef"}
	kept.inflight = append(kept.inflight, &mqtt5Outbound{id: 7, topic: "airc/room/a", body: []byte(`{}`)})
	kept.nextID = 7
	s.take("anonymous", "", true, time.Hour)
	expired, _, _ := s.take("expired", "guest:bob", true, time.Hour)
	expired.expiresAt = time.Now().Add(-time.Second)
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("sessions file", info, err)
	}

	restored := newMQTT5Store(context.Background())
	restored.path = path
	if err := restored.load(); err != nil {
		t.Fatal(err)
	}
	if len(restored.sessions) != 1 {
		t.Fatalf("restored %d sessions, want only the unexpired one with an owner", len(restored.sessions))
	}
	got := restored.sessions["kept"]
	if got == nil || got.owner != "guest:alice" || got.nextID != 7 || got.subs["airc/room/a"].after != kept.subs["airc/room/a"].after || len(got.inflight) != 1 || got.inflight[0].id != 7 || got.expiresAt.IsZero() {
		t.Fatalf("restored %+v", got)
	}
	// Another author is refused, and the session stays its owner's.
	if _, _, err := restored.take("kept", "guest:mallory", true, time.Hour); !errors.Is(err, errMQTTClientIDHeld) {
		t.Fatal("another author took a restored session:", err)
	}
	if _, present, err := restored.take("kept", "guest:alice", true, time.Hour); !present || err != nil {
		t.Fatal("the owner did not resume its session:", err)
	}

	missing := newMQTT5Store(context.Background())
	missing.path = filepath.Join(dir, "absent")
	if err := missing.load(); err != nil || len(missing.sessions) != 0 {
		t.Fatal("a missing file is an empty store", err)
	}
	for name, setup := range map[string]func(string) error{
		"shared": func(p string) error { return os.WriteFile(p, []byte(`{"version":1}`), 0o644) },
		"link":   func(p string) error { return os.Symlink(path, p) },
		"version": func(p string) error {
			return os.WriteFile(p, []byte(`{"version":2,"sessions":[]}`), 0o600)
		},
		"garbage": func(p string) error { return os.WriteFile(p, []byte(`not json`), 0o600) },
		"fifo":    func(p string) error { return syscall.Mkfifo(p, 0o600) },
	} {
		p := filepath.Join(dir, name)
		if err := setup(p); err != nil {
			t.Fatal(err)
		}
		bad := newMQTT5Store(context.Background())
		bad.path = p
		if err := bad.load(); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

// A saved session outside the bounds a live one keeps is dropped on load:
// one that would make nextIDLocked search forever, hold more than its
// share, or follow what its filter does not name.
func TestMQTT5SnapshotBounds(t *testing.T) {
	future := time.Now().Add(time.Hour)
	good := func(id string) mqtt5SnapshotSession {
		return mqtt5SnapshotSession{ClientID: id, Owner: "a:alice", Expiry: time.Hour, ExpiresAt: future, Subs: []mqtt5SnapshotSub{{Filter: "airc/room/a", Kind: mqtt5Room, Target: "#a", QoS: 1}}}
	}
	flood := good("flood")
	for i := range 65535 {
		flood.Inflight = append(flood.Inflight, mqtt5SnapshotOutbound{ID: uint16(i + 1), Topic: "airc/room/a"})
	}
	mismatched := good("mismatched")
	mismatched.Subs[0].Target = "#elsewhere"
	repeated := good("repeated")
	repeated.Inflight = []mqtt5SnapshotOutbound{{ID: 3}, {ID: 3}}
	sessions := []mqtt5SnapshotSession{good("kept"), flood, mismatched, repeated}
	for i := range config.DefaultMQTTSessionsPerAuthor + 4 {
		sessions = append(sessions, good(fmt.Sprintf("many-%d", i)))
	}
	data, err := json.Marshal(mqtt5Snapshot{Version: 1, Sessions: sessions})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newMQTT5Store(context.Background())
	s.path = path
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"flood", "mismatched", "repeated"} {
		if s.sessions[id] != nil {
			t.Errorf("loaded %s", id)
		}
	}
	if s.sessions["kept"] == nil || len(s.sessions) != config.DefaultMQTTSessionsPerAuthor {
		t.Fatalf("loaded %d sessions, want %d of alice's", len(s.sessions), config.DefaultMQTTSessionsPerAuthor)
	}
	// The per-author cap holds for new sessions too.
	if _, _, err := s.take("one-more", "a:alice", false, time.Hour); !errors.Is(err, errMQTTOwnerSessions) {
		t.Fatal("an author exceeded its sessions:", err)
	}
	if _, _, err := s.take("bobs", "a:bob", false, time.Hour); err != nil {
		t.Fatal("another author refused:", err)
	}
}

// A shared subscription group's position is saved with the sessions, and
// restored while a session still subscribes to it.
func TestMQTT5SnapshotKeepsGroupPositions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions")
	s := newMQTT5Store(context.Background())
	s.path = path
	member, _, _ := s.take("worker-1", "a:pool", false, time.Hour)
	member.subs["$share/w/airc/room/a"] = &mqtt5Sub{filter: "$share/w/airc/room/a", kind: mqtt5Room, target: "#a", qos: 1, group: "w"}
	kept := mqtt5GroupKey("a:pool", "w", mqtt5Room, "#a")
	orphan := mqtt5GroupKey("a:pool", "gone", mqtt5Room, "#a")
	s.groups[kept] = &mqtt5Group{owner: "a:pool", name: "w", kind: mqtt5Room, target: "#a", after: "0123456789abcdef0123456789abcdef"}
	s.groups[orphan] = &mqtt5Group{owner: "a:pool", name: "gone", kind: mqtt5Room, target: "#a", after: "0123456789abcdef0123456789abcdef"}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	restored := newMQTT5Store(context.Background())
	restored.path = path
	if err := restored.load(); err != nil {
		t.Fatal(err)
	}
	if g := restored.groups[kept]; g == nil || g.after != "0123456789abcdef0123456789abcdef" || restored.sessions["worker-1"].subs["$share/w/airc/room/a"].group != "w" {
		t.Fatalf("restored groups %v", restored.groups)
	}
	if restored.groups[orphan] != nil {
		t.Fatal("restored a group no session subscribes to")
	}
}
