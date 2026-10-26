//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpHTTPFixture is an MCP session with a gateway process serving one TLS
// mcp_http listener.
type mcpHTTPFixture struct {
	client *mcp.ClientSession
	config config.Config
}

func newMCPHTTPFixture(t *testing.T, options *mcp.ClientOptions) mcpHTTPFixture {
	t.Helper()
	// Build before opening native clients so compilation cannot consume their idle budget.
	currentGatewayBinary(t)
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	cert := testtls.New(t)
	c.Listeners = []config.Listener{{ID: "mcp", Protocol: "mcp_http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: dispatch.ImplementedOperations(), Limits: fixtureListenerLimits()}}
	p := startGateway(t, gatewayBinary(t), c)
	address := mcpGatewayAddress(t, p)
	httpTransport := &http.Transport{TLSClientConfig: cert.Client}
	t.Cleanup(httpTransport.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "gateway-mcp-test", Version: "1"}, options).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "https://" + address + "/mcp", HTTPClient: &http.Client{Transport: httpTransport}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return mcpHTTPFixture{client: session, config: c}
}

func mcpGatewayAddress(t *testing.T, p *gatewayProcess) string {
	t.Helper()
	select {
	case data := <-p.ready:
		var ready struct {
			State     string `json:"state"`
			Listeners []struct {
				Address string `json:"address"`
			} `json:"listeners"`
		}
		if err := json.Unmarshal(data, &ready); err != nil || ready.State != "ready" || len(ready.Listeners) != 1 {
			t.Fatalf("invalid MCP ready record %s: %v", data, err)
		}
		return ready.Listeners[0].Address
	case <-p.done:
		t.Fatalf("MCP gateway exited before ready: %v; stderr=%s", p.err, p.stderr.String())
	case <-time.After(8 * time.Second):
		t.Fatalf("MCP gateway failed to become ready; stderr=%s", p.stderr.String())
	}
	return ""
}

func mcpCallParams(t *testing.T, request core.Request) *mcp.CallToolParams {
	t.Helper()
	var name string
	switch request.Operation {
	case core.Send:
		name = "send"
	case core.HistoryRead:
		name = "read"
	case core.Watch:
		name = "wait"
	case core.Check:
		name = "check"
	case core.ReceiptGet, core.CapabilitiesGet:
		name = "status"
	default:
		t.Fatalf("unsupported MCP operation in fixture: %s", request.Operation)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(body, &args); err != nil {
		t.Fatal(err)
	}
	delete(args, "version")
	delete(args, "operation")
	if args["author"], err = json.Marshal(map[string]string{"nick": fixtureAuthor, "token": fixtureAccount}); err != nil {
		t.Fatal(err)
	}
	if request.Operation == core.ReceiptGet {
		args["lookup_id"], err = json.Marshal(request.LookupID)
		if err != nil {
			t.Fatal(err)
		}
	}
	body, err = json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(body)}
}

func mcpStructured(t *testing.T, call *mcp.CallToolResult) []byte {
	t.Helper()
	if call.IsError {
		t.Fatalf("MCP tool error: %#v", call.StructuredContent)
	}
	data, err := json.Marshal(call.StructuredContent)
	if err != nil || len(data) == 0 {
		t.Fatalf("MCP tool returned invalid structured content: %v", err)
	}
	return data
}
