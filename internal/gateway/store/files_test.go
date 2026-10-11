//go:build darwin || linux

package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("new journal schema", version, err)
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

// A journal written before the squashed schema is refused, whatever it holds,
// and left exactly as it was so the previous binary can still drain it.
func TestPreviousJournalIsRefusedUnchanged(t *testing.T) {
	for _, version := range []int{1, 24, schemaVersion - 1} {
		c, _, _ := fixture(t)
		ctx := t.Context()
		path := filepath.Join(c.StateDir, "journal.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		for _, statement := range []string{
			"PRAGMA journal_mode=WAL",
			"CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT",
			`INSERT INTO metadata VALUES('configuration','{"Realm":"fixture","Bindings":null,"Principals":null}')`,
			"CREATE TABLE jobs (principal TEXT NOT NULL, binding TEXT NOT NULL, request_id TEXT NOT NULL, state TEXT NOT NULL, terminal_at INTEGER) STRICT",
			"INSERT INTO jobs VALUES('', '', 'request', 'retry_wait', NULL)",
			fmt.Sprintf("PRAGMA user_version=%d", version),
		} {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatal(statement, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s, err := Open(ctx, c)
		if err == nil {
			s.Close()
		}
		if !errors.Is(err, ErrPreviousJournal) || !strings.Contains(err.Error(), fmt.Sprintf("schema %d:", version)) || !strings.Contains(err.Error(), "drain it with the previous binary") {
			t.Fatalf("schema %d: %v", version, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("schema %d journal changed: %v", version, err)
		}
		db, err = sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var stored, jobs int
		if err := db.QueryRowContext(ctx, "SELECT (SELECT user_version FROM pragma_user_version),(SELECT count(*) FROM jobs)").Scan(&stored, &jobs); err != nil || stored != version || jobs != 1 {
			t.Fatal("previous journal changed", stored, jobs, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
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
