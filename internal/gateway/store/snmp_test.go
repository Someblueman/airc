//go:build darwin || linux

package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSNMPReplayRestartAndPrunedResult(t *testing.T) {
	c, _, _ := fixture(t)
	makeProof := snmpFactory(t, c, "fixture", "device")
	p := makeProof(2, 1000, -7, "世界\nbody")
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	job, created, err := admitSNMPTest(s, t.Context(), p, time.Unix(1000, 0))
	if err != nil || !created || job.Request.Message != "SNMP Alert\nMessage: 世界\nbody" {
		t.Fatal(job, created, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			j, created, err := admitSNMPTest(s, t.Context(), p, time.Unix(1001, 0))
			if err != nil || created || j.UpstreamID != job.UpstreamID {
				t.Error("duplicate changed work", created, err)
			}
		})
	}
	workers.Wait()
	if j, created, err := admitSNMPTest(s, t.Context(), makeProof(2, 1000, -7, "世界\nbody", 43), time.Unix(1001, 0)); err != nil || created || j.UpstreamID != job.UpstreamID {
		t.Fatal("envelope ID changed replay identity", created, err)
	}
	assertSNMPCount(t, s, "jobs", 1)
	assertSNMPCount(t, s, "snmp_engines", 1)
	assertSNMPCount(t, s, "snmp_owners", 1)
	assertSNMPCount(t, s, "snmp_seen", 1)
	if _, _, err := admitSNMPTest(s, t.Context(), makeProof(2, 1000, -7, "changed"), time.Unix(1001, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("changed tuple accepted", err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM jobs WHERE request_id=?", job.Request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := admitSNMPTest(s, t.Context(), p, time.Unix(1002, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("pruned result recreated", err)
	}
	assertSNMPCount(t, s, "jobs", 0)
}

func TestSNMPTimelinessSharedEngineAndReboot(t *testing.T) {
	c, _, _ := fixture(t)
	first := snmpFactory(t, c, "first", "device")
	second := snmpFactory(t, c, "second", "other")
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admit := func(pBoot, pTime, id int, wall int64) {
		t.Helper()
		if _, _, err := admitSNMPTest(s, t.Context(), first(pBoot, pTime, id, "ok"), time.Unix(wall, 0)); err != nil {
			t.Fatal(err)
		}
	}
	admit(2, 1000, 1, 1000)
	admit(2, 850, 2, 1000) // Exact 150-second boundary.
	if _, _, err := admitSNMPTest(s, t.Context(), second(2, 849, 3, "old"), time.Unix(1000, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("user reset engine clock", err)
	}
	if _, _, err := admitSNMPTest(s, t.Context(), first(2, 1000, 4, "old"), time.Unix(1151, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("idle clock did not advance", err)
	}
	// A newer received time resynchronizes the non-authoritative RFC clock.
	admit(2, 1001, 5, 1151)
	assertSNMPCount(t, s, "snmp_seen", 2) // 850 is now permanently outside the window.
	if _, _, err := admitSNMPTest(s, t.Context(), first(2, 850, 2, "ok"), time.Unix(1151, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("pruned replay became valid", err)
	}
	admit(3, 0, 6, 1152)
	if _, _, err := admitSNMPTest(s, t.Context(), second(2, 2000, 7, "old boot"), time.Unix(1152, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("older boot accepted", err)
	}
	if _, _, err := admitSNMPTest(s, t.Context(), first(3, 1, 8, "rollback"), time.Unix(1151, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("wall clock rollback accepted", err)
	}
	if _, _, err := admitSNMPTest(s, t.Context(), first(2147483647, 0, 9, "exhausted"), time.Unix(1152, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("exhausted boots accepted", err)
	}
	assertSNMPCount(t, s, "snmp_engines", 1)
	assertSNMPCount(t, s, "snmp_seen", 1)
}
