//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"
)

func TestSignedSyslogAtomicReplayOverlapAndReopen(t *testing.T) {
	c, _, _ := fixture(t)
	now := time.Unix(1000, 0)
	makeBatch := signedBatchFactory(t, c, now)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	b := makeBatch(1, 0, 1, []string{"first 雪", "second"}, now, now)
	jobs, err := admitSignedSyslogTest(s, t.Context(), b, now)
	if err != nil || len(jobs) != 2 {
		t.Fatal(jobs, err)
	}
	if jobs[0].Request.Message != "first 雪" || !jobs[0].Result.Durable {
		t.Fatal("exact durable intake lost")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay, err := admitSignedSyslogTest(s, t.Context(), b, now)
	if err != nil || replay[0].UpstreamID != jobs[0].UpstreamID || replay[1].UpstreamID != jobs[1].UpstreamID {
		t.Fatal("restart replay", err)
	}
	overlap := makeBatch(1, 1, 2, []string{"second", "third"}, now, now)
	more, err := admitSignedSyslogTest(s, t.Context(), overlap, now)
	if err != nil || more[0].UpstreamID != jobs[1].UpstreamID {
		t.Fatal("overlap", err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DELETE FROM jobs WHERE request_id=?", jobs[0].Request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := admitSignedSyslogTest(s, t.Context(), b, now); !errors.Is(err, ErrConflict) {
		t.Fatal("pruned replay recreated", err)
	}
	newBoot := makeBatch(2, 0, 1, []string{"new boot"}, now, now)
	if _, err := admitSignedSyslogTest(s, t.Context(), newBoot, now); err != nil {
		t.Fatal(err)
	}
	if _, err := admitSignedSyslogTest(s, t.Context(), overlap, now); !errors.Is(err, ErrConflict) {
		t.Fatal("old reboot accepted", err)
	}
}

func TestSignedSyslogCapacityRollsBackJobsAndWatermark(t *testing.T) {
	c, _, _ := fixture(t)
	c.Limits.PendingPerPrincipal = 1
	now := time.Unix(1000, 0)
	makeBatch := signedBatchFactory(t, c, now)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := makeBatch(1, 0, 1, []string{"first", "second"}, now, now)
	if _, err := admitSignedSyslogTest(s, t.Context(), b, now); !errors.Is(err, ErrCapacity) {
		t.Fatal("capacity", err)
	}
	for _, table := range []string{"jobs", "signed_syslog_state", "replay"} {
		var count int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial transaction", table, count, err)
		}
	}
	if _, err := admitSignedSyslogTest(s, t.Context(), makeBatch(1, 0, 1, []string{"first"}, now, now), now); err != nil {
		t.Fatal("failed batch consumed watermark", err)
	}
}
