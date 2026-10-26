//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP over streamable HTTP serves the contract as six tools; each takes the
// request's fields as arguments and answers with the operation's value as
// structured content, or AIRC's failure as a tool error. A session keeps an
// identified author, and "wait" with a progress token streams a watch as
// progress notifications. There is no search or context tool.
var _ = registerSuiteCarriers(suiteCarrier{name: "mcp_http", listener: config.Listener{Protocol: "mcp_http"}, dial: dialMCPSuite, results: true, tls: true, session: true, watch: true, wire: mcpSuiteWire})

// mcpSuiteTools names the tool for each operation the suite sends.
var mcpSuiteTools = map[core.Operation]string{
	core.Send: "send", core.HistoryRead: "read", core.Watch: "wait",
	core.ReceiptGet: "status", core.CapabilitiesGet: "status",
	core.AccountIdentify: "identify", core.AccountRegister: "register",
}

type mcpSuiteClient struct {
	session   *mcp.ClientSession
	endpoint  string
	transport *http.Transport
	proto     *atomic.Int32 // the HTTP major version of the last response
	progress  chan *mcp.ProgressNotificationParams
}

func dialMCPSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	c := &mcpSuiteClient{endpoint: "http://" + d.address + "/mcp", transport: &http.Transport{}, proto: new(atomic.Int32), progress: make(chan *mcp.ProgressNotificationParams, 64)}
	if d.tls != nil {
		c.endpoint, c.transport = "https://"+d.address+"/mcp", &http.Transport{TLSClientConfig: d.tls, ForceAttemptHTTP2: true}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "airc-suite", Version: "1"}, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
		select {
		case c.progress <- r.Params:
		default:
		}
	}})
	ctx, cancel := suiteContext(t)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.endpoint, HTTPClient: &http.Client{Transport: c}, MaxRetries: -1}, nil)
	if err != nil {
		c.transport.CloseIdleConnections()
		return nil, err
	}
	c.session = session
	return c, nil
}

// RoundTrip records each response's HTTP version.
func (c *mcpSuiteClient) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := c.transport.RoundTrip(r)
	if response != nil {
		c.proto.Store(int32(response.ProtoMajor))
	}
	return response, err
}

// mcpSuiteTool maps a contract request to its tool call: the operation
// picks the tool, and the other fields are its arguments. It reports false
// for a request that is not JSON.
func mcpSuiteTool(t *testing.T, request []byte) (core.Operation, *mcp.CallToolParams, bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if json.Unmarshal(request, &fields) != nil {
		return "", nil, false
	}
	var operation core.Operation
	_ = json.Unmarshal(fields["operation"], &operation)
	name, ok := mcpSuiteTools[operation]
	if !ok {
		t.Fatalf("no MCP tool for %q", operation)
	}
	delete(fields, "version")
	delete(fields, "operation")
	return operation, &mcp.CallToolParams{Name: name, Arguments: fields}, true
}

func (c *mcpSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	operation, params, ok := mcpSuiteTool(t, request)
	if !ok {
		return c.unparsed(t, request)
	}
	ctx, cancel := suiteContext(t)
	defer cancel()
	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		// Arguments the tool's schema refuses fail the call itself.
		if wire := new(jsonrpc.Error); errors.As(err, &wire) && wire.Code == jsonrpc.CodeInvalidParams {
			return suiteReply{failure: &core.Failure{Code: core.InvalidRequest}}
		}
		if errors.Is(err, mcp.ErrConnectionClosed) {
			return suiteReply{closed: true}
		}
		t.Fatal(err)
	}
	return mcpSuiteReply(t, operation, result)
}

// mcpSuiteReply puts a tool's structured content where the DetachedResult
// every other carrier returns holds it.
func mcpSuiteReply(t *testing.T, operation core.Operation, result *mcp.CallToolResult) suiteReply {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		var failure core.Failure
		if err := json.Unmarshal(data, &failure); err != nil || failure.Code == "" {
			t.Fatalf("tool error %s: %v", data, err)
		}
		return suiteReply{failure: &failure}
	}
	detached := &dispatch.DetachedResult{Version: 1, Operation: operation}
	var value any
	switch operation {
	case core.HistoryRead, core.Watch:
		detached.Page = new(backend.HistoryPage)
		value = detached.Page
	case core.CapabilitiesGet:
		detached.Capabilities = new(dispatch.Capabilities)
		value = detached.Capabilities
	case core.AccountIdentify, core.AccountRegister:
		detached.Account = new(backend.Account)
		value = detached.Account
	default:
		detached.Result = new(core.Result)
		value = detached.Result
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("%s result %s: %v", operation, data, err)
	}
	return suiteReply{result: detached}
}

// unparsed posts a tools/call whose arguments are not JSON on the session:
// the MCP server refuses the body (400 Bad Request, or a JSON-RPC parse
// error).
func (c *mcpSuiteClient) unparsed(t *testing.T, request []byte) suiteReply {
	t.Helper()
	ctx, cancel := suiteContext(t)
	defer cancel()
	body := append(append([]byte(`{"jsonrpc":"2.0","id":"unparsed","method":"tools/call","params":{"name":"send","arguments":`), request...), "}}"...)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", c.session.ID())
	response, err := c.transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	var reply struct {
		Error *jsonrpc.Error `json:"error"`
	}
	if response.StatusCode != http.StatusBadRequest && (json.Unmarshal(data, &reply) != nil || reply.Error == nil || reply.Error.Code != jsonrpc.CodeParseError) {
		t.Fatalf("unparsed tool call answered %d %s", response.StatusCode, data)
	}
	return suiteReply{failure: &core.Failure{Code: core.InvalidRequest}}
}

func (c *mcpSuiteClient) send(t *testing.T, request []byte) { c.call(t, request) }

func (c *mcpSuiteClient) close() {
	_ = c.session.Close()
	c.transport.CloseIdleConnections()
}

func (c *mcpSuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	_, params, _ := mcpSuiteTool(t, request)
	params.SetProgressToken("suite-watch")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.session.CallTool(ctx, params)
	}()
	return func() *backend.HistoryPage {
		t.Helper()
		select {
		case p := <-c.progress:
			var page backend.HistoryPage
			if p.ProgressToken != "suite-watch" || json.Unmarshal([]byte(p.Message), &page) != nil {
				t.Fatalf("progress %+v", p)
			}
			return &page
		case <-done:
			return nil
		case <-time.After(20 * time.Second):
			t.Fatal("no progress page")
			return nil
		}
	}, cancel
}

// mcpSuiteWire covers MCP's own surface: exactly the six author tools,
// identify and session-scoped status, history pages and their cursors, and
// HTTP/1.1 or HTTP/2.
func mcpSuiteWire(t *testing.T, e *suiteEnv) {
	client := e.dial(t).(*mcpSuiteClient)
	t.Run("tools", func(t *testing.T) {
		// The six author tools are all there is: an operation's own name is
		// no tool, and nothing reaches intake.
		tools, err := client.session.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tool := range tools.Tools {
			names = append(names, tool.Name)
		}
		slices.Sort(names)
		if !reflect.DeepEqual(names, []string{"check", "identify", "read", "register", "send", "status", "wait"}) {
			t.Fatal("MCP tools", names)
		}
		for _, operation := range append(dispatch.ImplementedOperations(), core.CapabilitiesGet) {
			if slices.Contains(names, string(operation)) {
				continue
			}
			id := e.id("removed")
			if result, err := client.session.CallTool(t.Context(), &mcp.CallToolParams{Name: string(operation), Arguments: map[string]any{"request_id": id}}); err == nil && !result.IsError {
				t.Fatalf("tool %s answered %+v", operation, result)
			}
			e.noJob(t, id)
		}
		if r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("status"), "operation": core.ReceiptGet, "lookup_id": "never-sent"}, e.guest("wire"), ""))); r.failure == nil {
			t.Fatalf("status of an unknown request: %v", r)
		}
		// status without a lookup ID is the listener's capabilities.
		r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("capabilities"), "operation": core.CapabilitiesGet}, e.guest("wire"), "")))
		if r.result == nil || r.result.Capabilities == nil || r.result.Capabilities.Version != 1 || !slices.Contains(r.result.Capabilities.Operations, core.Send) || !slices.Contains(r.result.Capabilities.Operations, core.HistoryRead) {
			t.Fatalf("listener capabilities: %v", r)
		}
	})
	t.Run("identify", func(t *testing.T) {
		// identify refuses a wrong token; once identified, the session's
		// status lookups are scoped to its account.
		session := e.dial(t).(*mcpSuiteClient)
		if r := session.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("identify"), "operation": core.AccountIdentify}, "alice", wrongToken))); r.failure == nil {
			t.Fatalf("identify with a wrong token: %v", r)
		}
		if r := session.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("identify"), "operation": core.AccountIdentify}, "alice", aliceToken))); r.result == nil || r.result.Account == nil {
			t.Fatalf("identify: %v", r)
		}
		id, text := e.id("session"), e.text("session")
		e.admitted(t, session.call(t, sendBody(t, id, "#wire", text, "", "")), id)
		e.posted(t, id, "#wire", text, "alice", aliceToken)
		if r := session.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("status"), "operation": core.ReceiptGet, "lookup_id": id}, "", ""))); r.result == nil || r.result.Result == nil || r.result.Result.RequestID != id || r.result.Result.State != core.Accepted {
			t.Fatalf("status as the identified account: %v", r)
		}
	})
	t.Run("history_pages", func(t *testing.T) {
		// read pages whole records to a caught-up cursor; wait without a
		// progress token answers one page; a read cursor is no wait cursor.
		nick := e.guest("pages")
		for i := range 3 {
			id, text := e.id("page"), e.text(fmt.Sprint("page ", i))
			e.admitted(t, client.call(t, sendBody(t, id, "#wire", text, nick, "")), id)
			e.posted(t, id, "#wire", text, nick, "")
		}
		native := nativeHistory(t, e.socket, "#wire", 64)
		room := map[string]any{"kind": "room", "name": "#wire"}
		page := func(operation core.Operation, limit int, cursor string) suiteReply {
			fields := map[string]any{"request_id": e.id("read"), "operation": operation, "target": room, "limit": limit}
			if cursor == "" {
				fields["start"] = "retained"
			} else {
				fields["cursor"] = cursor
			}
			return client.call(t, mustBody(t, plainRequest(fields, nick, "")))
		}
		cursor := ""
		for _, want := range [][]*irc.MessageMetadata{native[:len(native)-1], native[len(native)-1:], nil} {
			r := page(core.HistoryRead, len(native)-1, cursor)
			status := "ok"
			if len(want) == len(native)-1 {
				status = "more"
			}
			if r.result == nil || r.result.Page == nil || r.result.Page.Status != status || r.result.Page.Gap || r.result.Page.Cursor == "" || len(r.result.Page.Messages) != len(want) || len(want) > 0 && !reflect.DeepEqual(r.result.Page.Messages, want) {
				t.Fatalf("read page: %v, aircd holds %+v", r, want)
			}
			cursor = r.result.Page.Cursor
		}
		if r := page(core.Watch, 2, ""); r.result == nil || r.result.Page == nil || r.result.Page.Cursor == "" || !reflect.DeepEqual(r.result.Page.Messages, native[:2]) {
			t.Fatalf("wait page: %v", r)
		}
		if r := page(core.Watch, 2, cursor); r.failure == nil {
			t.Fatalf("wait took a read cursor: %v", r)
		}
	})
	t.Run("http_versions", func(t *testing.T) {
		// Plaintext is HTTP/1.1; TLS negotiates HTTP/2.
		if _, err := client.session.ListTools(t.Context(), nil); err != nil || client.proto.Load() != 1 {
			t.Fatalf("plaintext MCP used HTTP/%d: %v", client.proto.Load(), err)
		}
		secure := e.dialWith(t, suiteDial{address: e.secure, tls: e.cert.Client.Clone()}).(*mcpSuiteClient)
		if _, err := secure.session.ListTools(t.Context(), nil); err != nil || secure.proto.Load() != 2 {
			t.Fatalf("TLS MCP used HTTP/%d: %v", secure.proto.Load(), err)
		}
	})
}
