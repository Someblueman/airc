//go:build darwin || linux

package store

import (
	"testing"
	"time"
)

func TestSignedSyslogFreshnessAndFailedWrites(t *testing.T) {
	c, _, _ := fixture(t)
	c.Retry.LifetimeSeconds = 120
	now := time.Unix(1000, 0)
	makeBatch := signedBatchFactory(t, c, now)
	for _, name := range []string{"expired_block", "future_block", "expired_record", "before_reboot", "future_reboot", "read_only", "bad_body"} {
		t.Run(name, func(t *testing.T) {
			local, _, _ := fixture(t)
			local.Retry.LifetimeSeconds = 120
			// Keep the author upstream while isolating storage.
			local.Upstream = c.Upstream
			s, err := Open(t.Context(), local)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			stamp, started, recordStamp := now, now, now
			body := "exact"
			switch name {
			case "expired_block":
				stamp = time.Unix(880, 0)
				started = stamp
				recordStamp = stamp
			case "future_block":
				stamp = time.Unix(1061, 0)
				recordStamp = stamp
			case "expired_record":
				recordStamp = time.Unix(880, 0)
				started = recordStamp
			case "before_reboot":
				recordStamp = time.Unix(939, 0)
			case "future_reboot":
				started = time.Unix(1061, 0)
			case "read_only":
				if _, err := s.db.ExecContext(t.Context(), "PRAGMA query_only=ON"); err != nil {
					t.Fatal(err)
				}
			case "bad_body":
				body = "first\r\nsecond"
			}
			b := makeBatch(1, 0, 1, []string{body}, stamp, started, recordStamp)
			if _, err := admitSignedSyslogTest(s, t.Context(), b, now); err == nil {
				t.Fatal("invalid or unwritable batch accepted")
			}
			for _, table := range []string{"jobs", "signed_syslog_state", "replay"} {
				var count int
				if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatal("failure persisted partial state", table, count, err)
				}
			}
		})
	}
}

func TestSignedSyslogMigrationPreservesJobsAndOSCORE(t *testing.T) {
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
	rewindTableMigrations(t, s, 12)
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
		t.Fatal("OSCORE changed", after, err)
	}
	checkAuthorKeyedJournal(t, s, c, identity)
	var version, count int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM signed_syslog_state").Scan(&count); err != nil || count != 0 {
		t.Fatal("migration invented replay state", count, err)
	}
}
