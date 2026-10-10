//go:build darwin || linux

package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSystemVQueueMigrationPreservesJobsAndReplayState(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyKafkaPosition(t, s)
	posix := queueOwnershipRecord()
	if err := s.SavePOSIXQueueOwnership(t.Context(), posix, nil); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("%064x", 42)
	fingerprint := fmt.Sprintf("%064x", 43)
	for _, table := range []string{"irc_events", "matrix_events"} {
		// Fixed migration fixture table names; never constructed from input.
		if _, err := s.db.ExecContext(t.Context(), "INSERT INTO "+table+" VALUES(?,?,?,?,?,?)", marker, fingerprint, "device", "backend", int64(2000), 123); err != nil {
			t.Fatal(err)
		}
	}
	rewindTableMigrations(t, s, 17)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retained, err := s.Lookup(t.Context(), identity, job.Request.RequestID)
	if err != nil || retained.UpstreamID != job.UpstreamID || retained.Request.Message != job.Request.Message || retained.Result.State != job.Result.State {
		t.Fatal("changed job", retained, err)
	}
	checkLegacyKafkaPosition(t, s)
	owned, err := s.LoadPOSIXQueueOwnership(t.Context(), posix.Listener)
	if err != nil || owned != posix {
		t.Fatal("changed POSIX ownership", owned, err)
	}
	for _, table := range []string{"irc_events", "matrix_events"} {
		var id, fp, principal, binding string
		var seconds, nanos int64
		err := s.db.QueryRowContext(t.Context(), "SELECT request_id,fingerprint,principal,binding,expires_seconds,expires_nanos FROM "+table).Scan(&id, &fp, &principal, &binding, &seconds, &nanos)
		if err != nil || id != marker || fp != fingerprint || principal != "device" || binding != "backend" || seconds != 2000 || nanos != 123 {
			t.Fatal("changed replay mark", table, err)
		}
	}
	if _, err := s.LoadSystemVQueueOwnership(t.Context(), "sysv"); !errors.Is(err, ErrNotFound) {
		t.Fatal("invented ownership", err)
	}
	if err := s.SaveSystemVQueueOwnership(t.Context(), systemVQueueRecord(), nil); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
}
