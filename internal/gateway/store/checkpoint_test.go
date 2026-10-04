//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPinnedReaderBackpressuresIntake(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// An isolated SQLite reader deliberately ignores gateway ownership to pin a
	// WAL snapshot. No production command is allowed to bypass the owner lock.
	db, err := sql.Open("sqlite", filepath.Join(c.StateDir, "journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value string
	if err := tx.QueryRow("SELECT value FROM metadata").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, request(), 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(ctx); err == nil {
		t.Fatal("pinned checkpoint reported success")
	}
	r := request()
	r.RequestID = "second"
	if _, _, err := s.Admit(ctx, identity, r, 0, time.Unix(1000, 0)); err == nil {
		t.Fatal("intake ignored failed checkpoint")
	}
	if _, err := s.Lookup(ctx, identity, r.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("blocked intake persisted", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, new, err := s.Admit(ctx, identity, r, 0, time.Unix(1000, 0)); err != nil || !new {
		t.Fatal("intake did not recover after checkpoint", err)
	}
}
