//go:build darwin || linux

package adapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpLifecycleFixture serves MCP with two sessions in all and one per source
// address. Index i connects from loopback address 127.0.0.(i+1).
type mcpLifecycleFixture struct {
	owner   *MCPHTTP
	journal *store.Store
	url     string
}

func mcpLifecycle(t *testing.T) mcpLifecycleFixture {
	t.Helper()
	f := newRouteFixture(t)
	d := f.listener(t, config.Listener{ID: "mcp", Protocol: "mcp_http", Operations: []core.Operation{core.Send, core.HistoryRead, core.Watch, core.ReceiptGet}})
	server, owner, err := MCPHTTPServer(d, make(chan struct{}, 16), 2, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler)
	t.Cleanup(func() { owner.Close(); httpServer.Close() })
	return mcpLifecycleFixture{owner: owner, journal: f.s, url: httpServer.URL + "/mcp"}
}

// mcpClient is an HTTP client whose connections come from 127.0.0.(index+1).
// A host without that loopback alias skips the test.
func mcpClient(t *testing.T, index int) *http.Client {
	t.Helper()
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, byte(index+1))}
	probe, err := net.ListenTCP("tcp", local)
	if err != nil {
		t.Skipf("loopback alias %s unavailable: %v", local.IP, err)
	}
	_ = probe.Close()
	dialer := &net.Dialer{LocalAddr: local, Timeout: 2 * time.Second}
	return &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: false}}
}

func (f mcpLifecycleFixture) connect(t *testing.T, ctx context.Context, index int) (*mcp.ClientSession, error) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "lifecycle-test", Version: "1"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.url, HTTPClient: mcpClient(t, index), MaxRetries: -1}, nil)
}

func waitMCPOwnership(t *testing.T, owner *MCPHTTP, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		owner.mu.Lock()
		current := owner.global
		owner.mu.Unlock()
		servers := owner.handler.Sessions()
		if current == count && servers == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP ownership=%d servers=%d want=%d", current, servers, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
