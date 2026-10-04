//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAtomicReplayFailureAndCommitFailure(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	i, e := int64(1000), int64(1100)
	r := request()
	r.IssuedAt = &i
	r.ExpiresAt = &e
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fixture_failure BEFORE INSERT ON replay BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, r, 1160, time.Unix(1000, 0)); err == nil {
		t.Fatal("failed replay transaction claimed queued")
	}
	if _, err := s.Lookup(ctx, identity, r.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial job persisted", err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fixture_failure;
CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY);
CREATE TABLE fixture_child(id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER fixture_commit AFTER INSERT ON jobs BEGIN INSERT INTO fixture_child VALUES(1); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, r, 1160, time.Unix(1000, 0)); err == nil {
		t.Fatal("failed commit claimed queued")
	}
	if _, err := s.Lookup(ctx, identity, r.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed commit left job", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fixture_commit"); err != nil {
		t.Fatal(err)
	}
	if _, new, err := s.Admit(ctx, identity, r, 1160, time.Unix(1000, 0)); err != nil || !new {
		t.Fatal("valid atomic intake failed", err)
	}
	var replay int64
	if err := s.db.QueryRowContext(ctx, "SELECT replay_until FROM replay").Scan(&replay); err != nil || replay != 1160 {
		t.Fatal("replay not committed together", err)
	}
}
