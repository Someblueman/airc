//go:build linux && (arm64 || amd64)

package service

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testmqueue"
)

func posixIngressFixture(t *testing.T) (config.Config, *store.Store, config.Listener) {
	t.Helper()
	c := lifecycleConfig(t)
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	l := config.Listener{ID: "queue", Protocol: "posix_mqueue", Address: fmt.Sprintf("/agw-service-%x", nonce), POSIXQueue: &config.POSIXQueue{Depth: 1}, Operations: []core.Operation{core.Send}}
	c.Listeners = []config.Listener{l}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return c, s, l
}

func TestPOSIXIngressRegistersAndRecreates(t *testing.T) {
	testmqueue.Require(t)
	_, s, l := posixIngressFixture(t)
	q, err := openPOSIXIngress(t.Context(), s, l)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	record, err := s.LoadPOSIXQueueOwnership(t.Context(), l.ID)
	if err != nil || record.QueueDevice != q.Identity().Device || record.QueueInode != q.Identity().Inode || record.Name != l.Address {
		t.Fatal("ownership not registered", record, err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = openPOSIXIngress(t.Context(), s, l)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	record, err = s.LoadPOSIXQueueOwnership(t.Context(), l.ID)
	if err != nil || record.QueueInode != q.Identity().Inode {
		t.Fatal("new ownership not persisted", record, err)
	}
	changed := l
	changed.Address += "-changed"
	if _, err := openPOSIXIngress(t.Context(), s, changed); !errors.Is(err, store.ErrConflict) {
		t.Fatal("listener name rebound", err)
	}
}

func TestPOSIXIngressPreservesUnknownQueue(t *testing.T) {
	testmqueue.Require(t)
	_, s, l := posixIngressFixture(t)
	unknown, err := adapter.CreatePOSIXQueue(l.Address, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer unknown.Close()
	if q, err := openPOSIXIngress(t.Context(), s, l); q != nil || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unknown queue adopted: %v %v", q, err)
	}
	retained, err := adapter.OpenPOSIXQueue(l.Address, 1)
	if err != nil {
		t.Fatal("unknown queue removed", err)
	}
	if retained.Identity() != unknown.Identity() {
		t.Fatal("unknown queue replaced")
	}
	_ = retained.Close()
}

func TestPOSIXIngressRegistrationFailureCleansOwnedQueue(t *testing.T) {
	testmqueue.Require(t)
	c, s, l := posixIngressFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(c.StateDir, "journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_queue_owner BEFORE INSERT ON posix_queue_ownership BEGIN SELECT RAISE(ABORT,'fixture registration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if q, err := openPOSIXIngress(t.Context(), s, l); q != nil || err == nil {
		t.Fatalf("failed registration returned queue: %v %v", q, err)
	}
	if q, err := adapter.OpenPOSIXQueue(l.Address, 1); q != nil || err == nil {
		if q != nil {
			_ = q.Close()
		}
		t.Fatal("failed registration leaked name", err)
	}
	if _, err := s.LoadPOSIXQueueOwnership(t.Context(), l.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed registration persisted", err)
	}
	if _, err := db.ExecContext(t.Context(), "DROP TRIGGER fail_queue_owner"); err != nil {
		t.Fatal(err)
	}
	healthy, err := openPOSIXIngress(t.Context(), s, l)
	if err != nil {
		t.Fatal("registration did not recover", err)
	}
	_ = healthy.Close()
}
