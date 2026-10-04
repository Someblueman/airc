//go:build darwin || linux

package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpLifecycleFixture struct {
	owner   *MCPHTTP
	journal *store.Store
	url     string
	tokens  []string
}

func mcpLifecycle(t *testing.T) mcpLifecycleFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "mcp-lifecycle", filepath.Join(root, "state")
	if err := os.Mkdir(c.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	account := filepath.Join(root, "account")
	if err := auth.CreateCredential(account); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "upstream", Network: "unix", Address: filepath.Join(root, "unused.sock"), Account: "fixture", AccountTokenFile: account}}
	f := mcpLifecycleFixture{}
	for _, id := range []string{"owner", "other"} {
		file := filepath.Join(root, id)
		if err := auth.CreateCredential(file); err != nil {
			t.Fatal(err)
		}
		token, err := auth.ReadToken(file)
		if err != nil {
			t.Fatal(err)
		}
		f.tokens = append(f.tokens, token)
		c.Principals = append(c.Principals, config.Principal{ID: id, Binding: "upstream", BearerTokenFile: file})
	}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	f.journal, err = store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.journal.Close() })
	d, err := dispatch.New(f.journal, v, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, owner, err := MCPHTTPServer(d, v, make(chan struct{}, 16), 2, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.owner = owner
	httpServer := httptest.NewServer(server.Handler)
	f.url = httpServer.URL + "/mcp"
	t.Cleanup(func() { owner.Close(); httpServer.Close() })
	return f
}

type mcpBearerTransport struct{ token string }

func (t mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(copy)
}

func (f mcpLifecycleFixture) connect(ctx context.Context, index int) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "lifecycle-test", Version: "1"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.url, HTTPClient: &http.Client{Transport: mcpBearerTransport{f.tokens[index]}}, MaxRetries: -1}, nil)
}

func waitMCPOwnership(t *testing.T, owner *MCPHTTP, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		owner.mu.Lock()
		current, servers := owner.global, len(owner.servers)
		owner.mu.Unlock()
		if current == count && servers == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP ownership=%d servers=%d want=%d", current, servers, count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
