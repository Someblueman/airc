//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"
)

func TestPOSIXQueueMigrationPreservesOSCOREAndJobs(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.AdmitAuthor(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	key := oscoreKey()
	if err := s.ProvisionOSCORE(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveOSCORESender(t.Context(), key, 8); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveOSCOREReceiver(t.Context(), key, 4, 3); err != nil {
		t.Fatal(err)
	}
	before, err := s.LoadOSCORE(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	rewindTableMigrations(t, s, 11)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || after != before {
		t.Fatal("OSCORE reset", after, err)
	}
	checkAuthorKeyedJournal(t, s, c, identity)
	record := queueOwnershipRecord()
	if _, err := s.LoadPOSIXQueueOwnership(t.Context(), record.Listener); !errors.Is(err, ErrNotFound) {
		t.Fatal("migration invented ownership", err)
	}
	if err := s.SavePOSIXQueueOwnership(t.Context(), record, nil); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
}
