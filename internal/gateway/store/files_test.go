//go:build darwin || linux

package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// Closing gives up ownership at once even while the process forks: a child
// holds a copy of the lock's descriptor until it execs.
func TestOwnershipReleasedWhileForking(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var forks sync.WaitGroup
	defer forks.Wait()
	defer stop.Store(true)
	for range 4 {
		forks.Go(func() {
			for !stop.Load() {
				_ = exec.Command("/bin/true").Run()
			}
		})
	}
	for i := range 500 {
		f, err := openFiles(dir)
		if err != nil {
			t.Fatalf("reopen %d: %v", i, err)
		}
		if err := f.close(); err != nil {
			t.Fatal(err)
		}
	}
}

// A journal written by an earlier gateway is refused, whatever it holds, and
// left exactly as it was so the previous binary can still drain it. Version
// 26 is the real schema that gateway created (testdata/schema-26.sql), with
// an accepted job in one of its per-kind outcome columns.
func TestPreviousJournalIsRefusedUnchanged(t *testing.T) {
	schema26, err := os.ReadFile(filepath.Join("testdata", "schema-26.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 24, 26} {
		c, _, _ := fixture(t)
		ctx := t.Context()
		path := filepath.Join(c.StateDir, "journal.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		statements := []string{
			"PRAGMA journal_mode=WAL",
			"CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT",
			`INSERT INTO metadata VALUES('configuration','{"Realm":"fixture","Bindings":null,"Principals":null}')`,
			"CREATE TABLE jobs (principal TEXT NOT NULL, binding TEXT NOT NULL, request_id TEXT NOT NULL, state TEXT NOT NULL, terminal_at INTEGER) STRICT",
			"INSERT INTO jobs VALUES('', '', 'request', 'retry_wait', NULL)",
			fmt.Sprintf("PRAGMA user_version=%d", version),
		}
		if version == 26 {
			statements = []string{
				"PRAGMA journal_mode=WAL",
				string(schema26),
				`INSERT INTO metadata VALUES('realm','fixture')`,
				`INSERT INTO jobs (author_scope,request_id,author_nick,registered,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at,accepted,terminal_at,topic_outcome) VALUES ('g:author','request','author',0,'f','request',X'7b7d','retry','accepted',1000,2000,0,604800,100,0,1,60,1000,1000,1,1001,X'7b7d')`,
			}
		}
		for _, statement := range statements {
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
