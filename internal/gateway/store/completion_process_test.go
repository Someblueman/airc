//go:build darwin || linux

package store

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCompletedAttemptSurvivesProcessDeath(t *testing.T) {
	c, identity, _ := fixture(t)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(c.StateDir), "config.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestJournalProcessHelper$")
	cmd.Env = append(os.Environ(), "AIRC_GATEWAY_TEST_CONFIG="+file, "AIRC_GATEWAY_TEST_PHASE=completed")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	s, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, err := s.LookupAuthor(context.Background(), identity, "request")
	if err != nil || j.Result.State != core.RetryWait || !j.Result.AcceptanceUncertain || j.Result.Accepted != nil || j.Result.SendAttempts != 1 || j.Result.LookupAttempts != 0 || j.Result.Failure == nil || !j.Result.Failure.Transient || j.Result.NextAction != "lookup" || j.Result.NextAttemptAt == nil || *j.Result.NextAttemptAt != 1010 {
		t.Fatal("committed completion lost evidence or schedule", j, err)
	}
}
