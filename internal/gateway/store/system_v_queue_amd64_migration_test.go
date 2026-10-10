//go:build darwin || linux

package store

import (
	"testing"
	"time"
)

func TestSystemVAMD64MigrationRetainsOwnershipAndJobs(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	job, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the exact version-22 ownership table; the other tables are
	// unchanged by migration 23. Historical migration18 remains immutable.
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE system_v_queue_ownership;"+migration18+"PRAGMA user_version=22;"); err != nil {
		t.Fatal(err)
	}
	linux := systemVQueueRecord()
	darwin := linux
	darwin.Listener, darwin.Platform, darwin.Key = "darwin", "darwin-arm64", linux.Key+1
	darwin.NamespaceDevice, darwin.NamespaceInode = 0, 0
	for _, record := range []SystemVQueueOwnership{linux, darwin} {
		if err := s.SaveSystemVQueueOwnership(t.Context(), record, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, before := range []SystemVQueueOwnership{linux, darwin} {
		after, err := s.LoadSystemVQueueOwnership(t.Context(), before.Listener)
		if err != nil || before != after {
			t.Fatal("migration changed ownership", before, after, err)
		}
	}
	retained, err := s.Lookup(t.Context(), identity, job.Request.RequestID)
	if err != nil || retained.UpstreamID != job.UpstreamID || retained.Request.Message != job.Request.Message || retained.Result.State != job.Result.State {
		t.Fatal("migration changed pending job", retained, err)
	}
	amd64 := linux
	amd64.Listener, amd64.Platform, amd64.Key = "amd64", "linux-amd64", linux.Key+2
	if err := s.SaveSystemVQueueOwnership(t.Context(), amd64, nil); err != nil {
		t.Fatal("AMD64 ownership refused", err)
	}
	loaded, err := s.LoadSystemVQueueOwnership(t.Context(), amd64.Listener)
	if err != nil || loaded != amd64 {
		t.Fatal("AMD64 ownership changed", loaded, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE system_v_queue_ownership SET namespace_inode=0 WHERE listener='amd64'"); err == nil {
		t.Fatal("database admitted Linux ownership without an IPC namespace")
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
}
