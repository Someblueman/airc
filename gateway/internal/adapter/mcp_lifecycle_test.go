//go:build darwin || linux

package adapter

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMCPPublicSDKSessionsBoundIsolationAndCleanup(t *testing.T) {
	f := mcpLifecycle(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	owner, err := f.connect(t, ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	waitMCPOwnership(t, f.owner, 1)
	if extra, err := f.connect(t, ctx, 0); err == nil {
		_ = extra.Close()
		t.Fatal("per-source session quota bypassed")
	}
	other, err := f.connect(t, ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	waitMCPOwnership(t, f.owner, 2)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Mcp-Session-Id", owner.ID())
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	response, err := mcpClient(t, 1).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("session takeover status=%d", response.StatusCode)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	waitMCPOwnership(t, f.owner, 1)
	reopened, err := f.connect(t, ctx, 0)
	if err != nil {
		t.Fatal("quota not released", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	waitMCPOwnership(t, f.owner, 2)
	f.owner.Close()
	waitMCPOwnership(t, f.owner, 0)
}
