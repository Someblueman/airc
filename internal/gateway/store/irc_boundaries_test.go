//go:build darwin || linux

package store

import (
	"testing"
	"time"
)

func TestIRCMigrationPreservesDeliveryAndExistingBrokerState(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	seedLegacyKafkaPosition(t, s)
	rewindTableMigrations(t, s, 15)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkAuthorKeyedJournal(t, s, c, identity)
	checkLegacyKafkaPosition(t, s)
	var count, version int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM irc_events").Scan(&count); err != nil || count != 0 {
		t.Fatal("invented IRC state", count, err)
	}
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("schema", version, err)
	}
}
