//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceStoredRetentionAndInclusiveReplayBoundary(t *testing.T) {
	c, identity, _ := fixture(t)
	c.Retry.LifetimeSeconds, c.Retry.TerminalRetentionSeconds = 10, 10
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := request()
	issued, expires := int64(1000), int64(1005)
	r.IssuedAt, r.ExpiresAt = &issued, &expires
	if _, _, err := s.Admit(ctx, identity, r, 1065, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Maintain(ctx, time.Unix(1005, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Retry.TerminalRetentionSeconds = 20
	s, err = Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := s.Maintain(ctx, time.Unix(1014, 0))
	if err != nil || counts.Results != 0 {
		t.Fatal("retention shortened", counts, err)
	}
	counts, err = s.Maintain(ctx, time.Unix(1015, 0))
	if err != nil || counts.Results != 1 || counts.Replay != 0 {
		t.Fatal("stored retention or replay lost", counts, err)
	}
	if _, _, err := s.Admit(ctx, identity, r, 1065, time.Unix(1015, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("pruned result recreated under retained replay marker", err)
	}
	counts, err = s.Maintain(ctx, time.Unix(1065, 0))
	if err != nil || counts.Replay != 0 {
		t.Fatal("inclusive replay boundary removed early", counts, err)
	}
	counts, err = s.Maintain(ctx, time.Unix(1066, 0))
	if err != nil || counts.Replay != 1 {
		t.Fatal("expired replay not reclaimed", counts, err)
	}
	if _, _, err := s.Admit(ctx, identity, r, 1065, time.Unix(1066, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("old expired signed intent became fresh", err)
	}
}

func TestMaintenanceFailedCommitRollsBackExpiryAndPruning(t *testing.T) {
	c, identity, _ := fixture(t)
	c.Retry.LifetimeSeconds, c.Retry.TerminalRetentionSeconds = 10, 10
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Admit(ctx, identity, request(), 0, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Maintain(ctx, time.Unix(1010, 0)); err != nil {
		t.Fatal(err)
	}
	second := request()
	second.RequestID = "second"
	if _, _, err := s.Admit(ctx, identity, second, 0, time.Unix(1010, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY);
CREATE TABLE fixture_child(id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER fixture_commit AFTER DELETE ON jobs BEGIN INSERT INTO fixture_child VALUES(1); END;`); err != nil {
		t.Fatal(err)
	}
	counts, err := s.Maintain(ctx, time.Unix(1020, 0))
	if err == nil || counts != (Maintenance{}) {
		t.Fatal("failed commit claimed maintenance", counts, err)
	}
	j, err := s.Lookup(ctx, identity, request().RequestID)
	if err != nil || j.Result.State != "expired" {
		t.Fatal("failed maintenance pruned the retained result", j, err)
	}
	j, err = s.Lookup(ctx, identity, second.RequestID)
	if err != nil || j.Result.State != "queued" {
		t.Fatal("failed maintenance persisted partial expiry", j, err)
	}
}
