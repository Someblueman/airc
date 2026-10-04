//go:build darwin || linux

package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"modernc.org/sqlite"
)

func TestProcessDeathPreservesCommittedIntake(t *testing.T) {
	c, identity, _ := fixture(t)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(c.StateDir), "config.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"committed", "uncommitted"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestJournalProcessHelper$")
		cmd.Env = append(os.Environ(), "AIRC_GATEWAY_TEST_CONFIG="+file, "AIRC_GATEWAY_TEST_PHASE="+phase)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child %s: %v %s", phase, err, output)
		}
		s, err := Open(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		j, err := s.Lookup(context.Background(), identity, "request")
		if err != nil || j.ReplayUntil != 1160 {
			t.Fatal("committed intake lost after process death", err)
		}
		var count int
		if err := s.db.QueryRowContext(context.Background(), "SELECT count(*) FROM replay").Scan(&count); err != nil || count != 1 {
			t.Fatal("replay and job diverged", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJournalProcessHelper(t *testing.T) {
	file := os.Getenv("AIRC_GATEWAY_TEST_CONFIG")
	if file == "" {
		t.Skip("subprocess helper")
	}
	c, err := config.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := v.Authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("AIRC_GATEWAY_TEST_PHASE") {
	case "committed", "granted", "revoked":
		r := request()
		i, e := int64(1000), int64(1100)
		r.IssuedAt = &i
		r.ExpiresAt = &e
		if _, _, err := s.Admit(context.Background(), identity, r, 1160, time.Unix(1000, 0)); err != nil {
			t.Fatal(err)
		}
		phase := os.Getenv("AIRC_GATEWAY_TEST_PHASE")
		if phase != "committed" {
			if _, err := s.Reserve(context.Background(), identity.ID(), "request", SendAttempt, time.Unix(1000, 0)); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "revoked" {
			if _, err := s.Revoke(context.Background(), identity.ID()); err != nil {
				t.Fatal(err)
			}
		}
	case "uncommitted":
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("DELETE FROM jobs"); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("DELETE FROM replay"); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unknown fixture phase")
	}
	// Deliberately bypass database/lock cleanup; the OS releases the owner lock.
	os.Exit(0)
}

func TestSQLiteFullDoesNotClaimResponsibility(t *testing.T) {
	c, identity, _ := fixture(t)
	ctx := context.Background()
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var pages int
	if err := s.db.QueryRowContext(context.Background(), "PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(context.Background(), "PRAGMA max_page_count="+strconv.Itoa(pages)); err != nil {
		t.Fatal(err)
	}
	r := request()
	r.Message = strings.Repeat("x", 4096)
	// Non-NUL valid text forces a new data page beyond the fixed page quota.
	_, _, err = s.Admit(ctx, identity, r, 0, time.Unix(1000, 0))
	var sqliteError *sqlite.Error
	if !errors.As(err, &sqliteError) || sqliteError.Code() != 13 {
		t.Fatal("expected real SQLITE_FULL without queued result", err)
	}
	if _, err := s.Lookup(ctx, identity, r.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("full database retained partial job", err)
	}
}
