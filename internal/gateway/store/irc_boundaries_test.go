//go:build darwin || linux

package store

import (
	"fmt"
	"testing"
	"time"
)

func TestIRCCapacityAndRetention(t *testing.T) {
	c, _, _ := fixture(t)
	stamp := time.Now().UTC().Add(-time.Second).Truncate(time.Millisecond)
	proof := ircProof(t, c, "device", "stable", "whole", stamp)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job, _, err := s.AdmitIRC(t.Context(), proof, stamp.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 16384; i++ {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO irc_events VALUES(?,?,?,?,?,?)", fmt.Sprintf("%064x", i), fmt.Sprintf("%064x", i), "device", "backend", proof.ReplayDeadline().Unix(), proof.ReplayDeadline().Nanosecond()); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	next := ircProof(t, c, "device", "next", "whole", stamp)
	if _, _, err := s.AdmitIRC(t.Context(), next, stamp.Add(time.Second)); err != ErrCapacity {
		t.Fatal("event capacity", err)
	}
	if j, created, err := s.AdmitIRC(t.Context(), proof, stamp.Add(time.Second)); err != nil || created || j.UpstreamID != job.UpstreamID {
		t.Fatal("replay at capacity", created, err)
	}
	// Live native jobs keep their global binding even after event freshness ends.
	counts, err := s.Maintain(t.Context(), proof.ReplayDeadline())
	if err != nil || counts.IRCEvents != 16383 {
		t.Fatal("retained job binding", counts, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM jobs WHERE request_id=?", job.Request.RequestID); err != nil {
		t.Fatal(err)
	}
	counts, err = s.Maintain(t.Context(), proof.ReplayDeadline().Add(-time.Nanosecond))
	if err != nil || counts.IRCEvents != 0 {
		t.Fatal("early mark deletion", counts, err)
	}
	counts, err = s.Maintain(t.Context(), proof.ReplayDeadline())
	if err != nil || counts.IRCEvents != 1 {
		t.Fatal("precise mark expiry", counts, err)
	}
	if _, _, err := s.AdmitIRC(t.Context(), proof, proof.ReplayDeadline()); err == nil {
		t.Fatal("expired proof resurrected")
	}
}

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
