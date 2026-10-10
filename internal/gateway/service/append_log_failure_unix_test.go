//go:build darwin || linux

package service

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestAppendLogMailboxMissingSubdirectoryStopsService(t *testing.T) {
	c := appendLogServiceConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	stop := context.AfterFunc(ctx, func() { _ = reader.Close() })
	defer stop()
	done := make(chan error, 1)
	go func() { err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = reader.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("failed poller did not join")
			}
		}
	})
	var ready struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(reader).Decode(&ready); err != nil || ready.State != "ready" {
		t.Fatal("not ready", err)
	}
	inbox := filepath.Join(c.Listeners[0].Address, "logs")
	if err := os.Remove(inbox); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		joined = true
		if err == nil {
			t.Fatal("missing directory treated as idle queue")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing directory did not stop polling")
	}
	if err := os.Mkdir(inbox, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := adapter.OpenAppendLogMailbox(c.Listeners[0].Address)
	if err != nil {
		t.Fatal("failed poller retained directory", err)
	}
	_ = m.Close()
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("failed poller retained journal", err)
	}
	_ = s.Close()
}
