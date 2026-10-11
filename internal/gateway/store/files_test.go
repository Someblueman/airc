//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnershipFilesAndSchema(t *testing.T) {
	c, _, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(ctx, c); err == nil {
		second.Close()
		t.Fatal("second owner accepted")
	}
	for _, name := range []string{"owner.lock", "journal.sqlite", "journal.sqlite-wal", "journal.sqlite-shm"} {
		info, err := os.Lstat(filepath.Join(c.StateDir, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("unsafe state file", name, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if next, err := Open(ctx, c); err == nil {
		next.Close()
		t.Fatal("future schema opened")
	}
}

func TestLostPinnedConnectionFailsClosed(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AdmitAuthor(ctx, identity, request(), 0, time.Unix(1000, 0)); err == nil {
		t.Fatal("failed connection silently reopened")
	}
}

func TestUnsafeState(t *testing.T) {
	c, _, _ := fixture(t)
	ctx := context.Background()
	if err := os.Chmod(c.StateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(ctx, c); err == nil {
		s.Close()
		t.Fatal("nonprivate state accepted")
	}
	if err := os.Chmod(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(c.StateDir), "outside")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(c.StateDir, "journal.sqlite-wal")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(ctx, c); err == nil {
		s.Close()
		t.Fatal("symlink state accepted")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "preserve" {
		t.Fatal("symlink target changed")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Realm = "other-realm"
	if s, err := Open(ctx, c); !errors.Is(err, ErrPinnedConfiguration) {
		if err == nil {
			s.Close()
		}
		t.Fatal("journal realm rebound", err)
	}
}
