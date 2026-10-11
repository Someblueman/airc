package adapter

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONRPCBodyReadErrorClassification(t *testing.T) {
	if code := rpcBodyErrorCode(&http.MaxBytesError{Limit: maxRPCBatchBytes}); code != -32600 {
		t.Fatalf("oversized body code=%d", code)
	}
	if code := rpcBodyErrorCode(io.ErrUnexpectedEOF); code != -32700 {
		t.Fatalf("truncated body code=%d", code)
	}
}

func TestJSONRPCEnvelopeStrictFieldsAndIDs(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		code  int
		hasID bool
	}{
		{"string id", `{"jsonrpc":"2.0","id":"x","method":"airc.capabilities"}`, 0, true},
		{"number id", `{"jsonrpc":"2.0","id":1.25,"method":"airc.capabilities"}`, 0, true},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"airc.capabilities"}`, 0, true},
		{"notification", `{"jsonrpc":"2.0","method":"airc.capabilities"}`, 0, false},
		{"duplicate outer", `{"jsonrpc":"2.0","id":"x","id":"y","method":"airc.capabilities"}`, -32600, false},
		{"unknown outer", `{"jsonrpc":"2.0","id":"x","method":"airc.capabilities","extra":1}`, -32600, false},
		{"case alias", `{"jsonrpc":"2.0","id":"x","Method":"airc.capabilities"}`, -32600, false},
		{"missing request params", `{"jsonrpc":"2.0","id":"x","method":"airc.request"}`, -32602, true},
		{"scalar params", `{"jsonrpc":"2.0","id":"x","method":"airc.request","params":null}`, -32600, false},
		{"array params", `{"jsonrpc":"2.0","id":"x","method":"airc.request","params":[]}`, -32602, true},
		{"malformed no id", `{"jsonrpc":"2.0","method":"airc.request","extra":1}`, -32600, false},
		{"invalid json", `{"jsonrpc":"2.0","method":`, -32700, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, code := decodeRPCMessage([]byte(test.body))
			if code != test.code || request.HasID != test.hasID {
				t.Fatalf("request=%+v code=%d", request, code)
			}
		})
	}
}

func TestJSONRPCNotificationsSuppressAllResponses(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"unknown"}`,
		`{"jsonrpc":"2.0","method":"airc.request","params":{"version":1,"request_id":"x","operation":"history.read"}}`,
		`{"jsonrpc":"2.0","method":"airc.request","params":{"version":1,"request_id":"x","operation":"send","target":null}}`,
	} {
		responses, batch, code := processRPCBody(t.Context(), rpcCaller{}, []byte(body))
		if code != 0 || batch || len(responses) != 0 {
			t.Fatalf("body=%s responses=%+v batch=%v code=%d", body, responses, batch, code)
		}
	}
}

func TestJSONRPCBatchBoundsAndNotificationOmission(t *testing.T) {
	responses, batch, code := processRPCBody(t.Context(), rpcCaller{}, []byte(`[{"jsonrpc":"2.0","method":"unknown"},{"jsonrpc":"2.0","method":"also-unknown","id":1}]`))
	if code != 0 || !batch || len(responses) != 1 || responses[0].Error == nil || string(responses[0].ID) != "1" {
		t.Fatalf("responses=%+v batch=%v code=%d", responses, batch, code)
	}
	items := make([]string, maxRPCBatchItems+1)
	for i := range items {
		items[i] = `{"jsonrpc":"2.0","method":"unknown","id":1}`
	}
	if _, batch, code := processRPCBody(t.Context(), rpcCaller{}, []byte("["+strings.Join(items, ",")+"]")); !batch || code != -32600 {
		t.Fatalf("oversized batch batch=%v code=%d", batch, code)
	}
	if _, batch, code := processRPCBody(t.Context(), rpcCaller{}, []byte(`[]`)); !batch || code != -32600 {
		t.Fatalf("empty batch batch=%v code=%d", batch, code)
	}
	if _, batch, code := processRPCBody(t.Context(), rpcCaller{}, []byte(`[{"jsonrpc":"2.0"}`)); !batch || code != -32700 {
		t.Fatalf("malformed batch batch=%v code=%d", batch, code)
	}
}

func TestJSONRPCHeadersRejectAmbiguousCredentials(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/rpc", nil)
	r.Header.Add("Content-Type", "application/json")
	r.Header.Add("Content-Type", "application/json")
	if validRPCHeaders(r, false) {
		t.Fatal("accepted duplicate Content-Type")
	}
	r = httptest.NewRequest(http.MethodPost, "/v1/rpc/events", nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Last-Event-ID", "cursor")
	if validRPCHeaders(r, true) {
		t.Fatal("accepted Last-Event-ID on JSON-RPC stream")
	}
	r.Header.Del("Last-Event-ID")
	r.Header.Set("Accept", "text/event-stream-evil")
	if validRPCHeaders(r, true) {
		t.Fatal("accepted non-SSE Accept token")
	}
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if !validRPCHeaders(r, true) {
		t.Fatal("rejected valid JSON media parameter")
	}
}
