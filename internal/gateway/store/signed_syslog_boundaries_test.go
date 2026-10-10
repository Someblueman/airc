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
	for _, name := range []string{"expired_block", "future_block", "expired_record", "before_reboot", "future_reboot", "read_only", "revoked", "bad_body"} {
		t.Run(name, func(t *testing.T) {
			local, _, _ := fixture(t)
			local.Retry.LifetimeSeconds = 120
			// Keep the authenticated batch's binding/authority while isolating storage.
			local.Principals = c.Principals
			local.Bindings = c.Bindings
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
			case "revoked":
				if _, err := s.Revoke(t.Context(), "device"); err != nil {
					t.Fatal(err)
				}
			case "bad_body":
				body = "first\r\nsecond"
			}
			b := makeBatch(1, 0, 1, []string{body}, stamp, started, recordStamp)
			if _, err := s.AdmitSignedSyslog(t.Context(), b, now); err == nil {
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
	job, _, err := s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
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
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE github_events; DROP TABLE system_v_queue_ownership; DROP TABLE matrix_events; DROP TABLE irc_events; DROP TABLE kafka_records; DROP TABLE kafka_intents; DROP TABLE kafka_positions; DROP TABLE snmp_seen; DROP TABLE snmp_owners; DROP TABLE snmp_engines; DROP TABLE signed_syslog_state; PRAGMA user_version=12"); err != nil {
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
	after, err := s.LoadOSCORE(t.Context(), key)
	if err != nil || after != before {
		t.Fatal("OSCORE changed", after, err)
	}
	retained, err := s.Lookup(t.Context(), identity, job.Request.RequestID)
	if err != nil || retained.UpstreamID != job.UpstreamID || retained.Request.Message != job.Request.Message {
		t.Fatal("job changed", err)
	}
	var version, count int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM signed_syslog_state").Scan(&count); err != nil || count != 0 {
		t.Fatal("migration invented replay state", count, err)
	}
}
