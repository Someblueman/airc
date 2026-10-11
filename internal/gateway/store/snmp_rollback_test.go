//go:build darwin || linux

package store

import (
	"testing"
	"time"
)

func TestSNMPFailedAdvancePreservesClockAndReplayMarks(t *testing.T) {
	c, _, _ := fixture(t)
	factory := snmpFactory(t, c, "fixture", "device")
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := factory(2, 1000, 1, "old")
	if _, _, err := admitSNMPTest(s, t.Context(), old, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_snmp BEFORE INSERT ON snmp_engines BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	// Advancing past 150 would prune the old replay mark and create another job.
	// A late SQL failure must roll back both, along with the clock update.
	if _, _, err := admitSNMPTest(s, t.Context(), factory(2, 1200, 2, "new"), time.Unix(1001, 0)); err == nil {
		t.Fatal("failed advance admitted")
	}
	assertSNMPCount(t, s, "jobs", 1)
	assertSNMPCount(t, s, "snmp_seen", 1)
	var latest uint32
	var seen int64
	if err := s.db.QueryRowContext(t.Context(), "SELECT latest_received,last_seen FROM snmp_engines").Scan(&latest, &seen); err != nil || latest != 1000 || seen != 1000 {
		t.Fatal("clock advanced on failure", latest, seen, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER refuse_snmp"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := admitSNMPTest(s, t.Context(), old, time.Unix(1001, 0)); err != nil || created {
		t.Fatal("rollback lost replay", created, err)
	}
}
