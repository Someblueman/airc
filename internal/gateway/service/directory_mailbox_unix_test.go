//go:build darwin || linux

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestDirectoryMailboxServicePollsAndJoinsRestart(t *testing.T) {
	c := directoryServiceConfig(t)
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		reader, writer := io.Pipe()
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
					t.Error("directory service did not join after cleanup")
				}
			}
		})
		// Closing the read side on timeout also unblocks readiness decoding.
		stop := context.AfterFunc(ctx, func() { _ = reader.Close() })
		var ready struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(reader).Decode(&ready); err != nil || ready.State != "ready" {
			t.Fatal("directory service readiness", err, ready)
		}
		if other, err := adapter.OpenDirectoryMailbox(c.Listeners[0].Address); err == nil {
			_ = other.Close()
			t.Fatal("running service did not own directory")
		}
		input := filepath.Join(c.Listeners[0].Address, "inbox", "request.json")
		temporary := filepath.Join(c.Listeners[0].Address, "producer.tmp")
		if err := os.WriteFile(temporary, []byte(`{"version":1,"correlation_id":"request","profile":"signed","envelope":"invalid"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temporary, input); err != nil {
			t.Fatal(err)
		}
		result := filepath.Join(c.Listeners[0].Address, "results", "request.json")
		for {
			data, err := os.ReadFile(result)
			if err == nil {
				var response adapter.FrameResponse
				if json.Unmarshal(data, &response) != nil || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
					t.Fatal("wrong polled refusal", response)
				}
				break
			}
			if !os.IsNotExist(err) || ctx.Err() != nil {
				t.Fatal("no polled result", err, ctx.Err())
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		select {
		case err := <-done:
			joined = true
			if err != nil {
				t.Fatal("directory shutdown", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("directory shutdown did not join")
		}
		stop()
		_ = reader.Close()
		if err := os.Remove(result); err != nil {
			t.Fatal(err)
		}
		s, err := store.Open(t.Context(), c)
		if err != nil {
			t.Fatal("joined shutdown retained journal", err)
		}
		if pending, err := s.HasPending(t.Context()); err != nil || pending {
			t.Fatal("refusal reached intake", pending, err)
		}
		_ = s.Close()
	}
}

func TestDirectoryMailboxStartupFailureReleasesOwner(t *testing.T) {
	c := directoryServiceConfig(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	c.Listeners = append(c.Listeners, config.Listener{ID: "occupied", Protocol: "http", Address: occupied.Addr().String(), Enabled: true})
	var out bytes.Buffer
	if err := Run(t.Context(), c, &out); err == nil || out.Len() != 0 {
		t.Fatal("failed startup claimed ready", err)
	}
	m, err := adapter.OpenDirectoryMailbox(c.Listeners[0].Address)
	if err != nil {
		t.Fatal("failed startup retained directory", err)
	}
	_ = m.Close()
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal("failed startup retained journal", err)
	}
	_ = s.Close()
}
