//go:build darwin || linux

package service

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestUnixListenerLifecycleCleansSocket(t *testing.T) {
	c := lifecycleConfig(t)
	parent, err := os.MkdirTemp("/tmp", "g-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "gateway.sock")
	c.Listeners = []config.Listener{{ID: "local", Protocol: "unix", Address: path, Enabled: true, Operations: []core.Operation{core.Send}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Run(ctx, c, &output) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("unix service cleanup did not join")
			}
		}
	})
	deadline := time.After(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		select {
		case <-deadline:
			select {
			case err := <-done:
				joined = true
				t.Fatalf("unix listener did not start: %v", err)
			default:
				t.Fatal("unix listener did not start")
			}
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unix service did not stop")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("unix socket remained after shutdown: %v", err)
	}
}

func TestUnixBindFailureCleansEarlierSocket(t *testing.T) {
	c := lifecycleConfig(t)
	parent, err := os.MkdirTemp("/tmp", "g-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "gateway.sock")
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	c.Listeners = []config.Listener{
		{ID: "unix", Protocol: "unix", Address: path, Enabled: true, Operations: []core.Operation{core.Send}},
		{ID: "occupied", Protocol: "http", Address: occupied.Addr().String(), Enabled: true, Operations: []core.Operation{core.Send}},
	}
	var output bytes.Buffer
	if err := Run(context.Background(), c, &output); err == nil || output.Len() != 0 {
		t.Fatal("failed startup claimed ready", err, output.String())
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("unix socket remained after failed startup: %v", err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal("failed startup leaked journal ownership", err)
	}
	_ = s.Close()
}
