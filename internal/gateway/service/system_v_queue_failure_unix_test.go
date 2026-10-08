//go:build (darwin || linux) && arm64

package service

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"golang.org/x/sys/unix"
	"path/filepath"
	"testing"
)

func TestSystemVIngressFailedRegistrationCleansOnlyCreatedQueue(t *testing.T) {
	c, s, key := systemVIngressFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(c.StateDir, "journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_system_v_owner BEFORE INSERT ON system_v_queue_ownership BEGIN SELECT RAISE(ABORT,'fixture registration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if q, err := openSystemVIngress(t.Context(), s, "sysv", key); q != nil || err == nil {
		t.Fatal("failed registration returned queue", q, err)
	}
	if q, err := sysvqueue.Open(key); q != nil || !errors.Is(err, unix.ENOENT) {
		t.Fatal("failed registration left ingress", q, err)
	}
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), "sysv"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed registration persisted", err)
	}
	if _, err := db.ExecContext(t.Context(), "DROP TRIGGER fail_system_v_owner"); err != nil {
		t.Fatal(err)
	}
	q, err := openSystemVIngress(t.Context(), s, "sysv", key)
	if err != nil {
		t.Fatal("registration did not recover", err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestSystemVIngressCancelledBeforeQueueCreation(t *testing.T) {
	_, s, key := systemVIngressFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if q, err := openSystemVIngress(ctx, s, "sysv", key); q != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(q, err)
	}
	if q, err := sysvqueue.Open(key); q != nil || !errors.Is(err, unix.ENOENT) {
		t.Fatal("cancelled startup created queue", q, err)
	}
	for _, call := range []func() error{
		func() error { _, err := openSystemVIngress(t.Context(), nil, "sysv", key); return err },
		func() error { _, err := openSystemVIngress(t.Context(), s, "sysv", 0); return err },
	} {
		if err := call(); !errors.Is(err, store.ErrInvalid) {
			t.Fatal(err)
		}
	}
}
