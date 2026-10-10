//go:build darwin || linux

package store

import (
	"fmt"
	"testing"
)

// Restore the table layout used by the v10-v17 fixtures. Unlike a partial list
// at each call site, this also removes tables added by later migrations.
// The v23 ownership-table rebuild is covered by its separate exact-schema test.
func rewindTableMigrations(t *testing.T, s *Store, version int) {
	t.Helper()
	if version < 10 || version > 17 {
		t.Fatal("unsupported migration fixture version", version)
	}
	tables := [...][]string{
		11: {"oscore_state"},
		12: {"posix_queue_ownership"},
		13: {"signed_syslog_state"},
		14: {"snmp_seen", "snmp_owners", "snmp_engines"},
		15: {"kafka_records", "kafka_intents", "kafka_positions"},
		16: {"irc_events"},
		17: {"matrix_events"},
		18: {"system_v_queue_ownership"},
		19: {"github_events"},
		20: {"matrix_progress"},
		21: {"couchdb_progress"},
		22: {"chirpstack_events"},
	}
	for migration := len(tables) - 1; migration > version; migration-- {
		for _, table := range tables[migration] {
			if _, err := s.db.ExecContext(t.Context(), "DROP TABLE "+table); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.db.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
		t.Fatal(err)
	}
}
