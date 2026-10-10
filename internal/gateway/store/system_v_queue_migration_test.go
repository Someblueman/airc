//go:build darwin || linux

package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSystemVQueueMigrationPreservesJobsAndReplayState(t *testing.T) {
	c, identity := kafkaStoreFixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	position, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE github_events; DROP TABLE system_v_queue_ownership; PRAGMA user_version=17"); err != nil {
		t.Fatal(err)
	}
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
	after, err := s.ProvisionKafka(t.Context(), "owner")
	if err != nil || after != position {
		t.Fatal("changed broker position", after, err)
	}
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
