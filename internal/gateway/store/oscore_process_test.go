//go:build darwin || linux

package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestOSCORESIGKILLPreservesReservedAndReplayState(t *testing.T) {
	c, _, _ := fixture(t)
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(c.StateDir), "oscore-config.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOSCOREStateProcessHelper$")
	cmd.Env = append(os.Environ(), "AIRC_OSCORE_STATE_TEST_CONFIG="+file)
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	readDone := make(chan struct{})
	defer func() {
		_ = cmd.Process.Kill()
		if !waited {
			_ = cmd.Wait()
		}
		<-readDone
	}()
	ready := make(chan string, 1)
	go func() {
		defer close(readDone)
		line, _ := bufio.NewReader(pipe).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "reserved\n" {
			t.Fatal("child failed before durable commit", line)
		}
	case <-ctx.Done():
		t.Fatal("child readiness timeout", ctx.Err())
	}
	if competing, err := Open(t.Context(), c); err == nil {
		_ = competing.Close()
		t.Fatal("competing owner admitted")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("SIGKILL unexpectedly succeeded")
	}
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.LoadOSCORE(t.Context(), oscoreKey())
	if err != nil || state != (OSCOREState{SenderEnd: 16, ReceiverInitialized: true, ReceiverSequence: 0, ReceiverWindow: 1}) {
		t.Fatal("restart lost state", state, err)
	}
	if err := s.ReserveOSCORESender(t.Context(), oscoreKey(), 16); !errors.Is(err, ErrConflict) {
		t.Fatal("restart reused reservation", err)
	}
	if err := s.ReserveOSCORESender(t.Context(), oscoreKey(), 24); err != nil {
		t.Fatal(err)
	}
}

func TestOSCOREStateProcessHelper(t *testing.T) {
	file := os.Getenv("AIRC_OSCORE_STATE_TEST_CONFIG")
	if file == "" {
		t.Skip("subprocess helper")
	}
	c, err := config.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProvisionOSCORE(t.Context(), oscoreKey()); err != nil {
		t.Fatal(err)
	}
	for _, end := range []uint64{8, 16} {
		if err := s.ReserveOSCORESender(t.Context(), oscoreKey(), end); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveOSCOREReceiver(t.Context(), oscoreKey(), 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.WriteString("reserved\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Hour) // Parent kills the process; no Close or checkpoint runs.
}
