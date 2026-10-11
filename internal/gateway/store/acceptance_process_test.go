//go:build darwin || linux

package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcceptanceProcessDeath(t *testing.T) {
	for _, phase := range []string{"saved", "save-failed"} {
		t.Run(phase, func(t *testing.T) {
			c, identity, _ := fixture(t)
			ctx := context.Background()
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(filepath.Dir(c.StateDir), "config.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestAcceptanceProcessHelper$")
			cmd.Env = append(os.Environ(), "AIRC_GATEWAY_ACCEPTANCE_TEST_CONFIG="+file, "AIRC_GATEWAY_ACCEPTANCE_TEST_PHASE="+phase)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child: %v %s", err, output)
			}
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			j, err := s.Lookup(ctx, identity, "request")
			if err != nil {
				t.Fatal(err)
			}
			if phase == "saved" {
				if j.Result.State != core.Accepted || j.Receipt == nil {
					t.Fatal("saved acceptance lost after abrupt exit")
				}
				if _, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, time.Unix(1001, 0)); !errors.Is(err, ErrNotReady) {
					t.Fatal("saved acceptance reopened for send")
				}
			} else if j.Result.State != core.RetryWait || j.Receipt != nil || !j.Result.DuplicateRisk || j.Result.SendAttempts != 1 {
				t.Fatal("failed-save crash boundary misrepresented", j)
			}
		})
	}
}

func TestAcceptanceProcessHelper(t *testing.T) {
	file := os.Getenv("AIRC_GATEWAY_ACCEPTANCE_TEST_CONFIG")
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
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s, err := Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Admit(ctx, identity, request(), 0, now); err != nil {
		t.Fatal(err)
	}
	g, err := s.Reserve(ctx, scopeOf(s, identity), "request", SendAttempt, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err := g.Consume(now)
	if err != nil {
		t.Fatal(err)
	}
	phase := os.Getenv("AIRC_GATEWAY_ACCEPTANCE_TEST_PHASE")
	if phase == "save-failed" {
		if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fixture_save BEFORE UPDATE OF receipt ON jobs BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.RecordAcceptance(ctx, g, receiptFor(a), now, now)
	if phase == "saved" && err != nil || phase == "save-failed" && err == nil {
		t.Fatal("unexpected save outcome", err)
	}
	if phase != "saved" && phase != "save-failed" {
		t.Fatal("unknown fixture phase")
	}
	os.Exit(0)
}
