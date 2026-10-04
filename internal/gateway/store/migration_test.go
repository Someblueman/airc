//go:build darwin || linux

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Someblueman/airc/internal/gateway/core"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionOneMigration(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(c.StateDir, "journal.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO metadata VALUES('configuration',?)", pinnedConfiguration(c)); err != nil {
		t.Fatal(err)
	}
	r := request()
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	fingerprint, _ := r.Fingerprint()
	upstream, _ := core.UpstreamID(c.Realm, identity.Binding(), identity.ID(), r.RequestID)
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs (principal,binding,request_id,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES (?,?,?,?,?,?,'retry','queued',1000,1100,1160,604800,100,100,1,60,1000,1000)`, identity.ID(), identity.Binding(), r.RequestID, fingerprint, upstream, data); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// SQLite's fixture-created main file must satisfy production ownership policy.
	if err := os.Chmod(filepath.Join(c.StateDir, "journal.sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, count int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatal("schema not migrated", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM authorization").Scan(&count); err != nil || count != 2 {
		t.Fatal("principals not migrated", err)
	}
	j, err := s.Lookup(ctx, identity, r.RequestID)
	if err != nil || j.UpstreamID != upstream || j.Result.ExpiresAt != 1100 || j.Request.Message != r.Message {
		t.Fatal("migration changed stored responsibility", j, err)
	}
}
