//go:build darwin || linux

package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// legacyUpstreamID is the pre-author-scope (v1) upstream identity, used only to
// build journals as older gateway versions wrote them.
func legacyUpstreamID(realm, binding, principal, requestID string) (string, error) {
	data, _ := json.Marshal([4]string{realm, binding, principal, requestID})
	sum := sha256.Sum256(append([]byte("airc-gateway-upstream-id-v1\x00"), data...))
	return fmt.Sprintf("%x", sum), nil
}

// rewindAuthorKeys replaces the author-keyed jobs and replay tables with empty
// tables in their exact pre-author-scope shape, built by the historical SQL.
// Rows do not survive: older layouts cannot hold author-keyed work.
func rewindAuthorKeys(t *testing.T, s *Store) {
	t.Helper()
	legacy, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	legacy.SetMaxOpenConns(1)
	if _, err := legacy.ExecContext(t.Context(), schema); err != nil {
		t.Fatal(err)
	}
	for _, migration := range journalMigrations[:authorKeyVersion-2] {
		if _, err := legacy.ExecContext(t.Context(), migration); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := legacy.QueryContext(t.Context(), "SELECT sql FROM sqlite_schema WHERE tbl_name IN ('jobs','replay') AND sql IS NOT NULL ORDER BY type<>'table',name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var statements []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			t.Fatal(err)
		}
		statements = append(statements, statement)
	}
	if err := rows.Err(); err != nil || len(statements) < 2 {
		t.Fatal("legacy job schema unavailable", err)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE jobs; DROP TABLE replay"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

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
	rewindAuthorKeys(t, s)
	if _, err := s.db.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
		t.Fatal(err)
	}
}

// checkAuthorKeyedJournal proves a migrated journal admits and finds work by
// author scope with the request ID as upstream identity. Rows from the rewound layout do
// not survive: rewindAuthorKeys could not carry them and migration drops them.
func checkAuthorKeyedJournal(t *testing.T, s *Store, c config.Config, identity Submission) {
	t.Helper()
	r := request()
	r.RequestID = "after-migration"
	job, created, err := s.AdmitAuthor(t.Context(), identity, r, 0, time.Unix(1000, 0))
	expected, _ := core.UpstreamID(fixtureScope(c), r.RequestID)
	if err != nil || !created || job.Scope != fixtureScope(c) || job.UpstreamID != expected {
		t.Fatal("migrated journal not author keyed", job, err)
	}
	if found, err := s.LookupAuthor(t.Context(), identity, r.RequestID); err != nil || found.UpstreamID != expected {
		t.Fatal("migrated journal lookup", found, err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 1 {
		t.Fatal("migration retained rewound rows", count, err)
	}
}
