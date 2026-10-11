//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestFIFOCLIAuthenticationRefusalEOFAndSignalsJoinOwnership(t *testing.T) {
	f := newFileMailboxFixture(t)
	c := configureFIFOFixture(t, f.config)
	for _, mode := range []struct {
		name   string
		signal syscall.Signal
	}{{"eof", 0}, {"interrupt", syscall.SIGINT}, {"terminate", syscall.SIGTERM}} {
		t.Run(mode.name, func(t *testing.T) {
			stream, stop, _ := startFIFO(t, f.binary, c, mode.signal, false)
			profiles := []string{"signed", "encrypted", "signed", "encrypted"}
			expected := map[string]bool{}
			for i, profile := range profiles {
				id := fmt.Sprintf("refusal-%d", i)
				expected[id] = true
				writeFrame(t, stream, frameRequest{Version: 1, CorrelationID: id, Profile: profile, Request: "{}"})
			}
			// Requests are outstanding together; every whole response must remain
			// correlated and bounded, regardless of handler completion order.
			for range profiles {
				response := readFrame(t, stream)
				if !expected[response.CorrelationID] || response.Error == nil || response.Error.Code != core.InvalidRequest {
					t.Fatal(response)
				}
				delete(expected, response.CorrelationID)
			}
			if mode.signal == 0 {
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
			}
			stop()
			journal, err := store.Open(t.Context(), c)
			if err != nil {
				t.Fatal("journal ownership retained", err)
			}
			pending, err := journal.HasPending(t.Context())
			closeErr := journal.Close()
			if pending || err != nil || closeErr != nil {
				t.Fatal("refusal admitted delivery", pending, err, closeErr)
			}
			pair, err := adapter.OpenFIFOStream(c.Listeners[0].Address)
			if err == nil {
				_ = pair.Close()
				t.Fatal("missing return peer unexpectedly admitted")
			}
			if !errors.Is(err, unix.ENXIO) {
				t.Fatal("FIFO ownership retained or unexpected open refusal", err)
			}
		})
	}
}

func TestFIFOStartupRejectsBeforeJournal(t *testing.T) {
	f := newFileMailboxFixture(t)
	c := configureFIFOFixture(t, f.config)
	c.StateDir = filepath.Join(filepath.Dir(c.StateDir), "fifo-absent-state")
	for _, name := range []string{"missing-return-reader", "ordinary-serve", "unsafe-node"} {
		t.Run(name, func(t *testing.T) {
			command := "serve-fifo"
			if name == "ordinary-serve" {
				command = "serve"
			}
			if name == "unsafe-node" {
				if err := os.Chmod(filepath.Join(c.Listeners[0].Address, "input.fifo"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(filepath.Dir(c.StateDir), "fifo-startup.json")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, f.binary, command, "--file", path)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err == nil || ctx.Err() != nil {
				t.Fatal("startup refusal", err, ctx.Err())
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatal("invalid refusal diagnostics", stdout.String(), stderr.String())
			}
			if _, err := os.Lstat(c.StateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refusal touched journal", err)
			}
		})
	}
}
