//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPHTTPNegotiatesHTTP1AndHTTP2(t *testing.T) {
	h1 := newMCPHTTPFixture(t)
	if _, err := h1.client.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := h1.proto.Load(); got != 1 {
		t.Fatalf("loopback MCP used HTTP/%d, want HTTP/1", got)
	}
	h2 := newMCPHTTPFixtureMode(t, true)
	if _, err := h2.client.ListTools(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := h2.proto.Load(); got != 2 {
		t.Fatalf("TLS MCP used HTTP/%d, want HTTP/2", got)
	}
}

func TestMCPHTTPSessionReconnectReplaysWithoutNativeDuplicate(t *testing.T) {
	f := newMCPHTTPFixture(t)
	request := core.Request{Version: 1, RequestID: "mcp-reconnect-send", Operation: core.Send,
		Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "MCP reconnect durable send"}
	admission := mcpResult(t, mcpCallRequest(t, f.client, request))
	if !admission.Durable {
		t.Fatalf("admission=%+v", admission)
	}
	first := mcpAwaitAccepted(t, f, request)
	if first.MessageID == "" || first.SendAttempts != 1 || first.LookupAttempts != 0 {
		t.Fatalf("first acceptance=%+v", first)
	}
	initial := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == first.MessageID })
	if initial.Message != request.Message || initial.Target != request.Target.Name {
		t.Fatalf("initial native message=%+v", initial)
	}
	for {
		select {
		case event := <-f.observer.Events():
			if message, ok := event.(*irc.MessageEvent); ok && message.ID == first.MessageID {
				t.Fatalf("native send was duplicated before reconnect: %+v", message)
			}
		default:
			goto drained
		}
	}
drained:
	if err := f.client.Close(); err != nil {
		t.Fatal(err)
	}
	reconnected := connectMCPHTTP(t, f, false, f.token, nil)
	f.client = reconnected
	replay := mcpResult(t, mcpCallRequest(t, reconnected, request))
	if replay.MessageID != first.MessageID || replay.State != core.Accepted || replay.SendAttempts != 1 || replay.LookupAttempts != 0 {
		t.Fatalf("replay changed durable result: first=%+v replay=%+v", first, replay)
	}
	select {
	case event := <-f.observer.Events():
		if message, ok := event.(*irc.MessageEvent); ok && message.ID == first.MessageID {
			t.Fatalf("native send was duplicated after reconnect: %+v", message)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestMCPHTTPRejectsSessionTakeoverByAnotherPrincipal(t *testing.T) {
	f := newMCPHTTPFixture(t)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.otherToken)
	request.Header.Set("Mcp-Session-Id", f.client.ID())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	transport := &http.Transport{TLSClientConfig: f.cert.Client}
	t.Cleanup(transport.CloseIdleConnections)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		var data json.RawMessage
		_ = json.NewDecoder(response.Body).Decode(&data)
		t.Fatalf("cross-principal session takeover status=%d body=%s", response.StatusCode, data)
	}
}

func TestMCPHTTPWatchJoinsOnGatewaySIGINT(t *testing.T) {
	progress := make(chan struct{}, 4)
	f := newMCPHTTPFixture(t, &mcp.ClientOptions{ProgressNotificationHandler: func(context.Context, *mcp.ProgressNotificationClientRequest) {
		select {
		case progress <- struct{}{}:
		default:
		}
	}})
	request := core.Request{Version: 1, RequestID: "mcp-watch-sigint", Operation: core.Watch,
		Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 10, Start: "recent"}
	params := mcpCallParams(t, request)
	params.SetProgressToken("mcp-watch-sigint-progress")
	done := make(chan error, 1)
	go func() {
		_, callErr := f.client.CallTool(t.Context(), params)
		done <- callErr
	}()
	select {
	case <-progress:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not establish before SIGINT")
	}
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("watch call did not join after gateway SIGINT")
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("gateway exited with SIGINT error: %v; stderr=%s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("gateway did not join after SIGINT")
	}
}
