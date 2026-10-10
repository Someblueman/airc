//go:build darwin || linux

package store

import (
	"strings"
	"testing"
)

func TestPageCapacityRefusalReportsRequestedAndActualBound(t *testing.T) {
	c, _, _ := fixture(t)
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO metadata VALUES(?,?)", "capacity-fixture", strings.Repeat("x", 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c.Limits.JournalBytes = 1 << 20
	reopened, err := Open(t.Context(), c)
	if reopened != nil {
		_ = reopened.Close()
		t.Fatal("oversized journal accepted")
	}
	if err == nil || !strings.Contains(err.Error(), "journal exceeds configured page capacity: requested 80 pages, actual ") {
		t.Fatal("missing capacity diagnostic", err)
	}
	// Refusal must release ownership and leave the original journal intact.
	c.Limits.JournalBytes = 256 << 20
	reopened, err = Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var value string
	if err := reopened.db.QueryRowContext(t.Context(), "SELECT value FROM metadata WHERE key=?", "capacity-fixture").Scan(&value); err != nil || len(value) != 1<<20 {
		t.Fatal("refusal changed journal", len(value), err)
	}
}
