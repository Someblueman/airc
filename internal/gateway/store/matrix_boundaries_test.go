//go:build darwin || linux

package store

import (
	"fmt"
	"testing"
	"time"
)

func TestMatrixMigrationPreservesDeliveryAndExistingBrokerState(t *testing.T) {
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
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO irc_events VALUES(?,?,?,?,?,?)", fmt.Sprintf("%064x", 42), fmt.Sprintf("%064x", 42), "device", "backend", int64(2000), 123); err != nil {
		t.Fatal(err)
	}
	rewindTableMigrations(t, s, 16)
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
	var retainedIRC string
	if err := s.db.QueryRowContext(t.Context(), "SELECT request_id FROM irc_events").Scan(&retainedIRC); err != nil || retainedIRC != fmt.Sprintf("%064x", 42) {
		t.Fatal("migration changed IRC replay binding", err)
	}
	var count, version int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM matrix_events").Scan(&count); err != nil || count != 0 {
		t.Fatal("invented Matrix state", count, err)
	}
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("schema", version, err)
	}
}
