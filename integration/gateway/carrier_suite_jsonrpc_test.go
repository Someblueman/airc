//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// JSON-RPC 2.0 rides the HTTP listener: POST /v1/rpc takes the contract
// request as airc.request params and answers with the DetachedResult, and
// POST /v1/rpc/events streams a watch as server-sent airc.page
// notifications.
var _ = registerSuiteCarriers(suiteCarrier{name: "jsonrpc", listener: config.Listener{Protocol: "http"}, dial: dialJSONRPCSuite, results: true, tls: true, reads: true, watch: true, wire: jsonRPCSuiteWire})

type rpcSuiteClient struct {
	base      string
	transport *http.Transport
	id        int
}

func dialJSONRPCSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	if d.tls != nil {
		return &rpcSuiteClient{base: "https://" + d.address, transport: &http.Transport{TLSClientConfig: d.tls}}, nil
	}
	return &rpcSuiteClient{base: "http://" + d.address, transport: &http.Transport{}}, nil
}

// rpcSuiteResponse is one JSON-RPC response or notification.
type rpcSuiteResponse struct {
	JSONRPC string                   `json:"jsonrpc"`
	ID      json.RawMessage          `json:"id"`
	Method  string                   `json:"method"`
	Result  *dispatch.DetachedResult `json:"result"`
	Error   *struct {
		Code int           `json:"code"`
		Data *core.Failure `json:"data"`
	} `json:"error"`
	Params *struct {
		ID   json.RawMessage      `json:"id"`
		Page *backend.HistoryPage `json:"page"`
	} `json:"params"`
}

// post sends a JSON body to path and returns the response, whose body the
// caller closes.
func (c *rpcSuiteClient) post(ctx context.Context, t *testing.T, path string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := (&http.Client{Transport: c.transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// exchange posts one JSON-RPC body to /v1/rpc and returns the status and
// the raw reply.
func (c *rpcSuiteClient) exchange(t *testing.T, body []byte, headers map[string]string) (int, []byte) {
	t.Helper()
	ctx, cancel := suiteContext(t)
	defer cancel()
	response := c.post(ctx, t, "/v1/rpc", body, headers)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func (c *rpcSuiteClient) envelope(request []byte) []byte {
	c.id++
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"method":"airc.request","params":%s}`, c.id, request)
}

// call answers a correlated result, or the error's AIRC failure. A protocol
// error (parse error, invalid request or params) carries no failure and is
// the contract's invalid_request.
func (c *rpcSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	status, data := c.exchange(t, c.envelope(request), nil)
	var response rpcSuiteResponse
	if err := json.Unmarshal(data, &response); err != nil || status != http.StatusOK || response.JSONRPC != "2.0" {
		t.Fatalf("JSON-RPC %d %s: %v", status, data, err)
	}
	id := string(response.ID)
	if response.Error != nil {
		if id != fmt.Sprint(c.id) && (id != "null" || response.Error.Code != -32700 && response.Error.Code != -32600) {
			t.Fatalf("uncorrelated error %s", data)
		}
		if response.Error.Data == nil {
			if response.Error.Code != -32700 && response.Error.Code != -32600 && response.Error.Code != -32602 {
				t.Fatalf("error without a failure %s", data)
			}
			return suiteReply{failure: &core.Failure{Code: core.InvalidRequest}}
		}
		return suiteReply{failure: response.Error.Data}
	}
	if id != fmt.Sprint(c.id) || response.Result == nil {
		t.Fatalf("JSON-RPC reply %s", data)
	}
	return suiteReply{result: response.Result}
}

func (c *rpcSuiteClient) send(t *testing.T, request []byte) { c.call(t, request) }

func (c *rpcSuiteClient) close() { c.transport.CloseIdleConnections() }

func (c *rpcSuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	response := c.post(ctx, t, "/v1/rpc/events", c.envelope(request), map[string]string{"Accept": "text/event-stream"})
	stop := func() { cancel(); _ = response.Body.Close() }
	t.Cleanup(stop)
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("JSON-RPC events %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader, id := bufio.NewReader(response.Body), fmt.Sprint(c.id)
	return func() *backend.HistoryPage {
		t.Helper()
		var data []byte
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil
			}
			if line = strings.TrimRight(line, "\r\n"); line == "" && len(data) > 0 {
				break
			}
			if value, ok := strings.CutPrefix(line, "data:"); ok {
				data = append(data, strings.TrimSpace(value)...)
			}
		}
		var event rpcSuiteResponse
		if err := json.Unmarshal(data, &event); err != nil || event.JSONRPC != "2.0" {
			t.Fatalf("SSE event %s: %v", data, err)
		}
		if event.Method != "airc.page" {
			return nil // the terminal response
		}
		if event.Params == nil || string(event.Params.ID) != id || event.Params.Page == nil {
			t.Fatalf("SSE page %s", data)
		}
		return event.Params.Page
	}, stop
}

// jsonRPCSuiteWire covers JSON-RPC's own envelope: IDs of every kind echoed,
// HTTP/1.1 and HTTP/2, notifications, batches and protocol errors.
func jsonRPCSuiteWire(t *testing.T, e *suiteEnv) {
	client := e.dial(t).(*rpcSuiteClient)
	rpc := func(t *testing.T, body string, headers map[string]string) (int, rpcSuiteResponse, []byte) {
		t.Helper()
		status, data := client.exchange(t, []byte(body), headers)
		var response rpcSuiteResponse
		if len(data) > 0 {
			if err := json.Unmarshal(data, &response); err != nil || response.JSONRPC != "2.0" {
				t.Fatalf("JSON-RPC %d %s: %v", status, data, err)
			}
		}
		return status, response, data
	}
	t.Run("ids", func(t *testing.T) {
		// airc.capabilities answers the author listener's operations under
		// any valid ID, echoed exactly; an over-long ID is refused.
		for _, id := range []string{`"cap-string"`, `17`, `null`, `1e400`, `9007199254740993.5`} {
			status, response, data := rpc(t, `{"jsonrpc":"2.0","id":`+id+`,"method":"airc.capabilities"}`, nil)
			var capabilities struct {
				Result struct {
					Operations []core.Operation `json:"operations"`
				} `json:"result"`
			}
			if status != http.StatusOK || string(response.ID) != id || response.Error != nil || json.Unmarshal(data, &capabilities) != nil || !reflect.DeepEqual(capabilities.Result.Operations, append(dispatch.ImplementedOperations(), core.AccountRegister)) {
				t.Fatalf("id %s: %d %s", id, status, data)
			}
		}
		if _, response, data := rpc(t, `{"jsonrpc":"2.0","id":"`+strings.Repeat("x", 257)+`","method":"airc.capabilities"}`, nil); response.Error == nil {
			t.Fatalf("over-long id answered %s", data)
		}
	})
	t.Run("http_versions", func(t *testing.T) {
		for proto, transport := range map[int]*http.Transport{1: {TLSClientConfig: e.cert.Client.Clone()}, 2: {TLSClientConfig: e.cert.Client.Clone(), ForceAttemptHTTP2: true}} {
			secure := &rpcSuiteClient{base: "https://" + e.secure, transport: transport}
			ctx, cancel := suiteContext(t)
			response := secure.post(ctx, t, "/v1/rpc", []byte(`{"jsonrpc":"2.0","id":1,"method":"airc.capabilities"}`), nil)
			_ = response.Body.Close()
			cancel()
			secure.close()
			if response.StatusCode != http.StatusOK || response.ProtoMajor != proto {
				t.Fatalf("HTTP/%d: %d %s", proto, response.StatusCode, response.Proto)
			}
		}
	})
	t.Run("notification", func(t *testing.T) {
		// A request without an ID is a notification: a send runs and
		// nothing is answered.
		id, text := e.id("notification"), e.text("notification")
		status, _, data := rpc(t, `{"jsonrpc":"2.0","method":"airc.request","params":`+string(sendBody(t, id, "#wire", text, e.guest("wire"), ""))+`}`, nil)
		if status != http.StatusNoContent || len(data) != 0 {
			t.Fatalf("notification answered %d %q", status, data)
		}
		e.posted(t, id, "#wire", text, e.guest("wire"), "")
	})
	t.Run("batch", func(t *testing.T) {
		status, data := client.exchange(t, []byte(`[{"jsonrpc":"2.0","id":"batch-one","method":"airc.capabilities","params":{}},{"jsonrpc":"2.0","id":2,"method":"airc.capabilities","params":{}}]`), nil)
		var replies []rpcSuiteResponse
		if err := json.Unmarshal(data, &replies); err != nil || status != http.StatusOK || len(replies) != 2 || string(replies[0].ID) != `"batch-one"` || string(replies[1].ID) != "2" || replies[0].Error != nil || replies[1].Error != nil {
			t.Fatalf("batch %d %s: %v", status, data, err)
		}
	})
	t.Run("protocol_errors", func(t *testing.T) {
		for _, c := range []struct {
			name, body, id string
			code           int
		}{
			{"unknown_method", `{"jsonrpc":"2.0","id":"bad","method":"unknown.method","params":{}}`, `"bad"`, -32601},
			{"duplicate_member", `{"jsonrpc":"2.0","id":"a","id":"b","method":"airc.capabilities"}`, "", 0},
			{"oversized", `{"jsonrpc":"2.0","id":"large","method":"airc.capabilities","params":{"padding":"` + strings.Repeat("x", 64<<10) + `"}}`, "null", -32600},
		} {
			status, response, data := rpc(t, c.body, nil)
			if status != http.StatusOK || response.Error == nil || c.code != 0 && (response.Error.Code != c.code || string(response.ID) != c.id) {
				t.Fatalf("%s: %d %s", c.name, status, data)
			}
		}
	})
	t.Run("access", func(t *testing.T) {
		// Without an author the listener's access is still discoverable; a
		// guest on a registered nickname cannot identify; a cross-origin
		// browser request is refused.
		if status, _, data := rpc(t, `{"jsonrpc":"2.0","id":1,"method":"airc.capabilities"}`, nil); status != http.StatusOK || !bytes.Contains(data, []byte(`"listener":{"guests":true`)) {
			t.Fatalf("anonymous capabilities %d %s", status, data)
		}
		identify := mustBody(t, plainRequest(map[string]any{"request_id": e.id("identify"), "operation": core.AccountIdentify}, "alice", ""))
		if r := client.call(t, identify); r.failure == nil || r.failure.Code != core.NickRegistered {
			t.Fatalf("guest identify on a registered nickname: %v", r)
		}
		if status, data := client.exchange(t, []byte(`{"jsonrpc":"2.0","id":1,"method":"airc.capabilities"}`), map[string]string{"Origin": "https://not-allowed.example"}); status != http.StatusForbidden && status != http.StatusUnauthorized {
			t.Fatalf("cross-origin request %d %s", status, data)
		}
	})
}
