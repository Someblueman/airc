//go:build darwin || linux

package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// A v24 journal migrates with its unfinished work intact: the job keeps the
// v2 upstream ID it may already have posted under, while new jobs use the
// client's request ID, which two authors may share.
func TestUpstreamIDMigrationKeepsJournaledJobs(t *testing.T) {
	c, _, _ := fixture(t)
	ctx := t.Context()
	path := filepath.Join(c.StateDir, "journal.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		t.Fatal(err)
	}
	for _, migration := range journalMigrations[:authorKeyVersion-1] {
		if _, err := db.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
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
	legacy, err := core.LegacyUpstreamID(c.Realm, "g:alice", r.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO jobs (author_scope,request_id,author_nick,registered,listener,fingerprint,upstream_id,request,policy,state,admitted_at,expires_at,replay_until,retention_seconds,send_limit,lookup_limit,backoff_seconds,max_backoff_seconds,next_send_at,next_lookup_at) VALUES ('g:alice',?,'alice',0,'http',?,?,?,'retry','retry_wait',1000,90000,90060,604800,100,100,1,60,1000,1000)`, r.RequestID, fingerprint, legacy, data); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, unique int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("version", version, err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_index_list('jobs') WHERE \"unique\" AND origin='u'").Scan(&unique); err != nil || unique != 0 {
		t.Fatal("upstream_id is still unique", unique, err)
	}
	kept, err := s.LookupAuthor(ctx, guest("alice", "ws"), r.RequestID)
	if err != nil || kept.UpstreamID != legacy || kept.Result.State != core.RetryWait {
		t.Fatalf("journaled job changed: %+v %v", kept, err)
	}
	now := time.Unix(1000, 0)
	again, created, err := s.AdmitAuthor(ctx, guest("alice", "ws"), request(), 0, now)
	if err != nil || created || again.UpstreamID != legacy {
		t.Fatalf("resubmission did not resolve to the journaled job: %+v created=%v %v", again, created, err)
	}
	for _, sub := range []Submission{guest("bob", "http"), registered(t, "alice", authorToken, "http")} {
		job, created, err := s.AdmitAuthor(ctx, sub, request(), 0, now)
		if err != nil || !created || job.UpstreamID != r.RequestID {
			t.Fatalf("%s: %+v created=%v %v", sub.Author.Nick, job, created, err)
		}
	}
}
