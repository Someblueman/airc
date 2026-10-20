package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testwait"
)

type echoInput struct {
	Text  string   `json:"text" jsonschema:"Text to echo"`
	Count int      `json:"count,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	Note  *string  `json:"note,omitempty"`
}

type echoOutput struct {
	Rows  []any `json:"rows"`
	Error any   `json:"error,omitempty"`
}

func testServer(block chan struct{}) *Server {
	s := NewServer(Implementation{Name: "test", Version: "1"}, "be brief")
	AddTool(s, &Tool{Name: "echo", Annotations: &Annotations{ReadOnlyHint: true}}, func(_ context.Context, _ *Call, in echoInput) (*Result, echoOutput, error) {
		if in.Text == "fail" {
			return nil, echoOutput{}, fmt.Errorf("refused")
		}
		return nil, echoOutput{Rows: []any{in.Text}}, nil
	})
	s.AddTool(&Tool{Name: "block", InputSchema: map[string]any{"type": "object"}, Handler: func(ctx context.Context, call *Call) *Result {
		if token := call.ProgressToken(); token != nil {
			_ = call.NotifyProgress(ctx, &ProgressParams{ProgressToken: token, Progress: 1, Message: "started"})
		}
		select {
		case <-ctx.Done():
			return &Result{Content: Text("cancelled"), IsError: true}
		case <-block:
			return &Result{Content: Text("released")}
		}
	}})
	return s
}

func handle(t *testing.T, s *Session, line string) map[string]any {
	t.Helper()
	m, err := ParseMessage([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	response := s.Handle(context.Background(), m, nil)
	if response == nil {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(response, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

func TestSessionLifecycleAndTypedTools(t *testing.T) {
	s := testServer(nil).NewSession()
	if r := handle(t, s, `{"jsonrpc":"2.0","id":0,"method":"tools/list"}`); r["error"] == nil {
		t.Fatal("tools/list before initialize", r)
	}
	if r := handle(t, s, `{"jsonrpc":"2.0","id":0,"method":"ping"}`); r["error"] != nil {
		t.Fatal("ping before initialize", r)
	}
	r := handle(t, s, initialize)
	result := r["result"].(map[string]any)
	if result["protocolVersion"] != supportedVersions[0] || result["instructions"] != "be brief" {
		t.Fatal("unsupported version not answered with the newest", result)
	}
	handle(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	tools := handle(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "block" {
		t.Fatal("tools not sorted by name", tools)
	}
	echo := tools[1].(map[string]any)
	schema, _ := json.Marshal(echo["inputSchema"])
	if !strings.Contains(string(schema), `"required":["text"]`) || !strings.Contains(string(schema), `"note":{"type":["null","string"]}`) || !strings.Contains(string(schema), `"description":"Text to echo"`) {
		t.Fatal("derived schema", string(schema))
	}
	for _, tc := range []struct{ arguments, want string }{
		{`{"text":"hi"}`, `"structuredContent":{"rows":["hi"]}`},
		{`{"text":"hi","tags":["a"],"note":null,"count":2}`, `"rows":["hi"]`},
		{`{"text":5}`, `type: 5 has type \"integer\", want \"string\"`},
		{`{"text":"hi","count":1.5}`, `want \"integer\"`},
		{`{"text":"hi","tags":[1]}`, `/properties/tags/items`},
		{`{}`, `missing properties: [\"text\"]`},
		{`{"text":"hi","Text":"x"}`, `unexpected additional properties [\"Text\"]`},
		{`{"text":"fail"}`, `"text":"refused"`},
	} {
		response, _ := json.Marshal(handle(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":`+tc.arguments+`}}`))
		if !strings.Contains(string(response), tc.want) {
			t.Errorf("%s: %s", tc.arguments, response)
		}
	}
	if r := handle(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"missing"}}`); r["error"].(map[string]any)["code"] != float64(CodeInvalidParams) {
		t.Fatal("unknown tool", r)
	}
	if r := handle(t, s, `{"jsonrpc":"2.0","id":5,"method":"no/such"}`); r["error"].(map[string]any)["code"] != float64(CodeMethodNotFound) {
		t.Fatal("unknown method", r)
	}
	if r := handle(t, s, `{"jsonrpc":"2.0","id":6,"method":"tools/call"}`); r["error"] == nil {
		t.Fatal("tools/call without params", r)
	}
	if r := handle(t, s, `{"jsonrpc":"2.0","id":7,"method":"notifications/initialized"}`); r["error"] == nil {
		t.Fatal("notification with an ID", r)
	}
	if _, err := ParseMessage([]byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)); err == nil {
		t.Fatal("batch accepted")
	}
}

func TestStdioCancellationAndEOF(t *testing.T) {
	block := make(chan struct{})
	input, feed := io.Pipe()
	output, sink := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- testServer(block).ServeStdio(context.Background(), input, sink) }()
	lines := bufio.NewScanner(output)
	next := func() string {
		t.Helper()
		if !lines.Scan() {
			t.Fatal("no output", lines.Err())
		}
		return lines.Text()
	}
	fmt.Fprintln(feed, initialize)
	next()
	fmt.Fprintln(feed, `not json`)
	if line := next(); !strings.Contains(line, `"code":-32700`) {
		t.Fatal("parse error", line)
	}
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"block","_meta":{"progressToken":7}}}`)
	if line := next(); !strings.Contains(line, `"method":"notifications/progress"`) || !strings.Contains(line, `"progressToken":7`) {
		t.Fatal("progress", line)
	}
	// A cancelled call is not answered; a later call still is.
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a"}}`)
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if line := next(); line != `{"jsonrpc":"2.0","id":2,"result":{}}` {
		t.Fatal("after cancellation", line)
	}
	// End of input cancels the outstanding call and returns nil.
	fmt.Fprintln(feed, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"block","_meta":{"progressToken":8}}}`)
	if line := next(); !strings.Contains(line, `"progressToken":8`) {
		t.Fatal("outstanding call", line)
	}
	go func() {
		for lines.Scan() {
		}
	}()
	feed.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EOF did not cancel the outstanding call")
	}
}

func httpTestServer(t *testing.T, timeout time.Duration) (*httptest.Server, *HTTPHandler, *atomic.Int32, chan struct{}) {
	block := make(chan struct{})
	var closed atomic.Int32
	h := &HTTPHandler{SessionTimeout: timeout, NewSession: func(*http.Request) (*Server, SessionHooks) {
		return testServer(block), SessionHooks{Closed: func(string, bool) { closed.Add(1) }}
	}}
	server := httptest.NewServer(h)
	t.Cleanup(func() { close(block); h.Close(); server.Close() })
	return server, h, &closed, block
}

func post(t *testing.T, url, session, body string, header ...string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		r.Header.Set("Mcp-Session-Id", session)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestHTTPSessionsAndStatusCodes(t *testing.T) {
	server, h, closed, _ := httpTestServer(t, time.Hour)
	url := server.URL + "/mcp"
	if r := post(t, url, "", initialize, "Accept", "application/json"); r.StatusCode != http.StatusBadRequest {
		t.Fatal("Accept without event-stream", r.StatusCode)
	}
	if r := post(t, url, "", initialize, "Content-Type", "text/plain"); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatal("Content-Type", r.StatusCode)
	}
	if r := post(t, url, "", initialize, "Mcp-Protocol-Version", "1999-01-01"); r.StatusCode != http.StatusBadRequest {
		t.Fatal("protocol version", r.StatusCode)
	}
	if r := post(t, url, "unknown", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != http.StatusNotFound {
		t.Fatal("unknown session", r.StatusCode)
	}
	r := post(t, url, "", initialize)
	id := r.Header.Get("Mcp-Session-Id")
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusOK || id == "" || r.Header.Get("Content-Type") != "text/event-stream" || !strings.HasPrefix(string(body), "event: message\ndata: {") {
		t.Fatalf("initialize status=%d id=%q body=%q", r.StatusCode, id, body)
	}
	if r := post(t, url, id, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.StatusCode != http.StatusAccepted {
		t.Fatal("notification", r.StatusCode)
	}
	if r := post(t, url, id, `{"jsonrpc":"2.0","id":2,"method":"no/such"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatal("unknown method", r.StatusCode)
	}
	// A failed initialize leaves no session behind, once its answer is sent.
	if r := post(t, url, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); r.StatusCode != http.StatusOK {
		t.Fatal("uninitialized session", r.StatusCode)
	}
	testwait.Until(t, 2*time.Second, "the uninitialized session to close", func() bool { return h.Sessions() == 1 && closed.Load() == 1 })
	// One GET stream per session; replay is unsupported.
	get := func(header ...string) *http.Response {
		request, _ := http.NewRequest(http.MethodGet, url, nil)
		request.Header.Set("Accept", "text/event-stream")
		request.Header.Set("Mcp-Session-Id", id)
		for i := 0; i+1 < len(header); i += 2 {
			request.Header.Set(header[i], header[i+1])
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	stream := get()
	if stream.StatusCode != http.StatusOK {
		t.Fatal("GET", stream.StatusCode)
	}
	if second := get(); second.StatusCode != http.StatusConflict {
		t.Fatal("second GET", second.StatusCode)
	}
	if replay := get("Last-Event-ID", "x_1"); replay.StatusCode != http.StatusBadRequest {
		t.Fatal("replay", replay.StatusCode)
	}
	// DELETE ends the session, its GET stream and its calls.
	request, _ := http.NewRequest(http.MethodDelete, url, nil)
	request.Header.Set("Mcp-Session-Id", id)
	deleted, err := http.DefaultClient.Do(request)
	if err != nil || deleted.StatusCode != http.StatusNoContent {
		t.Fatal("DELETE", deleted, err)
	}
	if _, err := io.ReadAll(stream.Body); err != nil {
		t.Fatal(err)
	}
	if h.Sessions() != 0 || closed.Load() != 2 {
		t.Fatal("sessions after DELETE", h.Sessions(), closed.Load())
	}
	if r := post(t, url, id, `{"jsonrpc":"2.0","id":3,"method":"ping"}`); r.StatusCode != http.StatusNotFound {
		t.Fatal("deleted session", r.StatusCode)
	}
}

func TestHTTPProgressCancellationAndIdleTimeout(t *testing.T) {
	server, h, closed, _ := httpTestServer(t, 200*time.Millisecond)
	url := server.URL + "/mcp"
	id := post(t, url, "", initialize).Header.Get("Mcp-Session-Id")
	call := make(chan string, 1)
	go func() {
		r := post(t, url, id, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"block","_meta":{"progressToken":"p"}}}`)
		body, _ := io.ReadAll(r.Body)
		call <- string(body)
	}()
	// The idle timer is paused while a POST runs.
	time.Sleep(400 * time.Millisecond)
	if h.Sessions() != 1 {
		t.Fatal("session expired during a call")
	}
	if r := post(t, url, id, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`); r.StatusCode != http.StatusAccepted {
		t.Fatal("cancel", r.StatusCode)
	}
	body := <-call
	if !strings.Contains(body, `"notifications/progress"`) || strings.Contains(body, `"id":9`) {
		t.Fatalf("cancelled call stream %q", body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.Sessions() != 0 || closed.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("idle session not closed", h.Sessions(), closed.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHTTPLoopbackRejectsForeignHost(t *testing.T) {
	server, _, _, _ := httpTestServer(t, time.Hour)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(initialize))
	request.Host = "evil.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	r, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatal("DNS rebinding", r.StatusCode)
	}
}
