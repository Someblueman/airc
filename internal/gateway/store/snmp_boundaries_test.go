//go:build darwin || linux

package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestSNMPFailureRollbackAndCurrentAuthority(t *testing.T) {
	for _, name := range []string{"capacity", "revocation", "SQL"} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := fixture(t)
			factory := snmpFactory(t, c, "fixture", "device")
			s, err := Open(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			switch name {
			case "capacity":
				s.config.Limits.Pending = 0
			case "revocation":
				if _, err := s.Revoke(t.Context(), "device"); err != nil {
					t.Fatal(err)
				}
			case "SQL":
				if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_snmp BEFORE INSERT ON snmp_engines BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := s.AdmitSNMP(t.Context(), factory(2, 1000, 1, "body"), time.Unix(1000, 0)); err == nil {
				t.Fatal("failure accepted")
			}
			for _, table := range []string{"jobs", "snmp_engines", "snmp_owners", "snmp_seen", "replay"} {
				assertSNMPCount(t, s, table, 0)
			}
		})
	}
	c, _, _ := fixture(t)
	factory := snmpFactory(t, c, "fixture", "device")
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.config.Principals[0].Operations = []core.Operation{core.ReceiptGet}
	if _, _, err := s.AdmitSNMP(t.Context(), factory(2, 1000, 1, "body"), time.Unix(1000, 0)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("removed authority accepted", err)
	}
	if _, _, err := s.AdmitSNMP(t.Context(), auth.VerifiedSNMPNotification{}, time.Unix(1000, 0)); !errors.Is(err, auth.ErrAuthentication) {
		t.Fatal("zero proof accepted", err)
	}
}

func TestSNMPOwnerCannotRebindAndKeyRotationPreservesClock(t *testing.T) {
	c, _, _ := fixture(t)
	factory := snmpFactory(t, c, "fixture", "device")
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.AdmitSNMP(t.Context(), factory(2, 1000, 1, "body"), time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	other := snmpFactory(t, c, "fixture", "other")
	if _, _, err := s.AdmitSNMP(t.Context(), other(2, 1001, 2, "body"), time.Unix(1001, 0)); !errors.Is(err, ErrConflict) {
		t.Fatal("owner rebound", err)
	}
	// A newly constructed verifier/key file cannot seed a fresh engine clock.
	rotated := snmpFactory(t, c, "fixture", "device", 0x32)
	if _, _, err := s.AdmitSNMP(t.Context(), rotated(2, 849, 3, "old"), time.Unix(1001, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal("rotation reset clock", err)
	}
	if _, created, err := s.AdmitSNMP(t.Context(), rotated(2, 1001, 4, "new key"), time.Unix(1001, 0)); err != nil || !created {
		t.Fatal("new localized key admission", created, err)
	}
	assertSNMPCount(t, s, "jobs", 2)
	assertSNMPCount(t, s, "snmp_owners", 1)
}

func TestSNMPMigrationPreservesEarlierState(t *testing.T) {
	c, identity, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Admit(t.Context(), identity, request(), 0, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	rewindTableMigrations(t, s, 13)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkAuthorKeyedJournal(t, s, c, identity)
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	for _, table := range []string{"snmp_engines", "snmp_owners", "snmp_seen", "signed_syslog_state"} {
		assertSNMPCount(t, s, table, 0)
	}
}
