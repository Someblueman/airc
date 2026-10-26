//go:build darwin || linux

package store

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestGrantSurvivesProcessDeath(t *testing.T) {
	for _, phase := range []string{"granted"} {
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
			cmd := exec.Command(os.Args[0], "-test.run=^TestJournalProcessHelper$")
			cmd.Env = append(os.Environ(), "AIRC_GATEWAY_TEST_CONFIG="+file, "AIRC_GATEWAY_TEST_PHASE="+phase)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child: %v %s", err, output)
			}
			s, err := Open(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			j, err := s.LookupAuthor(ctx, identity, "request")
			if err != nil || j.Result.State != core.RetryWait || j.Result.SendAttempts != 1 || !j.Result.DuplicateRisk {
				t.Fatal("crash lost committed marker", j, err)
			}
			g, err := s.Reserve(ctx, scopeOf(s, identity), "request", LookupAttempt, time.Unix(1001, 0))
			if err != nil {
				t.Fatal(err)
			}
			a, err := g.Consume(time.Unix(1001, 0))
			if err != nil || a.Job.Result.LookupAttempts != 1 {
				t.Fatal("fresh recovery grant invalid", err)
			}
		})
	}
}
