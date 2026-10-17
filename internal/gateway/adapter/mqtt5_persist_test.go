package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	// The owner resumes it; anyone else starts clean.
	if _, present, _ := restored.take("kept", "guest:mallory", true, time.Hour); present {
		t.Fatal("another author resumed a restored session")
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
