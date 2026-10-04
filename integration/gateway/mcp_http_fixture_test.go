//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpHTTPFixture struct {
	process    *gatewayProcess
	address    string
	client     *mcp.ClientSession
	token      string
	otherToken string
	config     config.Config
	cert       testtls.Certificate
	proto      *atomic.Int32
	observer   *irc.Client
}

func newMCPHTTPFixture(t *testing.T, options ...*mcp.ClientOptions) mcpHTTPFixture {
	return newMCPHTTPFixtureMode(t, false, options...)
}

func newMCPHTTPFixtureMode(t *testing.T, http2 bool, options ...*mcp.ClientOptions) mcpHTTPFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	observer := dial(t, native, "fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	c.Principals[0].Operations = ops
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	other := c.Principals[0]
	other.ID = "other"
	other.BearerTokenFile = filepath.Join(filepath.Dir(other.BearerTokenFile), "other.token")
	if err := auth.CreateCredential(other.BearerTokenFile); err != nil {
		t.Fatal(err)
	}
	c.Principals = append(c.Principals, other)
	cert := testtls.New(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "mcp", Protocol: "mcp_http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops}}
	p := startGateway(t, gatewayBinary(t), c)
	address := mcpGatewayAddress(t, p)
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	roundTripper := &bearerRoundTripper{base: &http.Transport{TLSClientConfig: cert.Client, ForceAttemptHTTP2: http2}, token: token, proto: new(atomic.Int32)}
	transport := &mcp.StreamableClientTransport{Endpoint: "https://" + address + "/mcp", HTTPClient: &http.Client{Transport: roundTripper}, MaxRetries: -1}
	t.Cleanup(func() { roundTripper.CloseIdleConnections() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var clientOptions *mcp.ClientOptions
	if len(options) > 0 {
		clientOptions = options[0]
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-mcp-test", Version: "1"}, clientOptions)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	otherToken, err := auth.ReadToken(other.BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	return mcpHTTPFixture{process: p, address: address, client: session, token: token, otherToken: otherToken, config: c, cert: cert, proto: roundTripper.proto, observer: observer}
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

type bearerRoundTripper struct {
	base  *http.Transport
	token string
	proto *atomic.Int32
}

func (t *bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	response, err := t.base.RoundTrip(copy)
	if response != nil && t.proto != nil {
		t.proto.Store(int32(response.ProtoMajor))
	}
	return response, err
}

func (t *bearerRoundTripper) CloseIdleConnections() { t.base.CloseIdleConnections() }

func connectMCPHTTP(t *testing.T, f mcpHTTPFixture, http2 bool, token string, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	roundTripper := &bearerRoundTripper{base: &http.Transport{TLSClientConfig: f.cert.Client, ForceAttemptHTTP2: http2}, token: token, proto: new(atomic.Int32)}
	transport := &mcp.StreamableClientTransport{Endpoint: "https://" + f.address + "/mcp", HTTPClient: &http.Client{Transport: roundTripper}, MaxRetries: -1}
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-mcp-reconnect", Version: "1"}, options)
	session, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(); roundTripper.CloseIdleConnections() })
	return session
}

func mcpCallRequest(t *testing.T, session *mcp.ClientSession, request core.Request) *mcp.CallToolResult {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: string(request.Operation), Arguments: json.RawMessage(body)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mcpResult(t *testing.T, call *mcp.CallToolResult) core.Result {
	t.Helper()
	if call.IsError {
		t.Fatalf("MCP tool error: %#v", call.StructuredContent)
	}
	data, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var result core.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err, string(data))
	}
	return result
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

func mcpSnapshot(t *testing.T, call *mcp.CallToolResult, operation core.Operation, target *core.Target) {
	t.Helper()
	data := mcpStructured(t, call)
	checkTarget := func(got core.Target) {
		if target == nil || got != *target {
			t.Fatalf("%s snapshot target=%+v want=%+v", operation, got, target)
		}
	}
	switch operation {
	case core.ContextRead:
		var value core.ContextResult
		if err := json.Unmarshal(data, &value); err != nil || value.Summary.TriggerID == "" {
			t.Fatalf("context snapshot: %s %v", data, err)
		}
	case core.PinsRead:
		var value core.PinsResult
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		checkTarget(value.Target)
	case core.RoomRead:
		var value core.RoomResult
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		checkTarget(value.Target)
	case core.WaitingRead:
		var value core.WaitingResult
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		checkTarget(value.Target)
	case core.PollResults:
		var value core.PollResult
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		checkTarget(value.Target)
	case core.MembersRead:
		var value core.MembersResult
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		checkTarget(value.Target)
		if value.Members == nil {
			t.Fatalf("members omitted explicit list: %s", data)
		}
	case core.TopicRead:
		var value core.TopicResult
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		checkTarget(value.Target)
	case core.HistoryRead, core.Search, core.Watch:
		var value backend.HistoryPage
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if value.Status == "" || value.Cursor == "" && len(value.Messages) != 0 {
			t.Fatalf("%s invalid page: %s", operation, data)
		}
	case core.Directory:
		var value backend.DirectorySnapshot
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if value.Agents == nil {
			t.Fatalf("directory omitted agents: %s", data)
		}
	default:
		t.Fatalf("unsupported snapshot operation %s", operation)
	}
}

func mcpAwaitAccepted(t *testing.T, f mcpHTTPFixture, request core.Request) core.Result {
	t.Helper()
	for i := range 80 {
		status := core.Request{Version: 1, RequestID: request.RequestID + "-status-" + strconv.Itoa(i), Operation: core.ReceiptGet, LookupID: request.RequestID}
		result := mcpResult(t, mcpCallRequest(t, f.client, status))
		if result.State == core.Accepted {
			return result
		}
		if result.State == core.Rejected || result.State == core.Expired || result.Failure != nil {
			t.Fatalf("%s delivery failed: %+v", request.Operation, result)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s delivery did not reach accepted", request.Operation)
	return core.Result{}
}
