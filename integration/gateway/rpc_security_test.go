//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func rpcRawHTTP(t *testing.T, f rpcHTTPFixture, body []byte, token, origin string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+"/v1/rpc", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		setAuthorHeaders(request, token)
	}
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := f.http1.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func TestRPCRejectsRegisteredNickOriginAndMalformedInputs(t *testing.T) {
	f := newRPCHTTPFixture(t, false)
	// A request without author headers is answered under the listener's
	// policy.
	status, body := rpcRawHTTP(t, f, rpcRequestBody(t, "missing-auth", "airc.capabilities", nil), "", "")
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"listener":{"guests":true`)) {
		t.Fatalf("missing auth status=%d body=%s", status, body)
	}
	// A guest on a registered nickname is refused by AIRC, as for an IRC
	// client.
	params := rpcRequestParams(core.Request{Version: 1, RequestID: "rpc-denied", Operation: core.AccountIdentify})
	params["author"] = map[string]string{"nick": fixtureAuthor}
	status, body = rpcRawHTTP(t, f, rpcRequestBody(t, "registered-nick", "airc.request", params), "", "")
	if status != http.StatusOK {
		t.Fatalf("registered nickname status=%d body=%s", status, body)
	}
	var denied rpcResponse
	if err := json.Unmarshal(body, &denied); err != nil || !bytes.Contains(denied.Error, []byte(core.NickRegistered)) {
		t.Fatalf("registered nickname JSON-RPC error=%s err=%v", body, err)
	}
	status, body = rpcRawHTTP(t, f, rpcRequestBody(t, "bad-origin", "airc.capabilities", nil), f.token, "https://not-allowed.example")
	if status != http.StatusForbidden && status != http.StatusUnauthorized {
		t.Fatalf("bad origin status=%d body=%s", status, body)
	}
	malformed := []byte(`{"jsonrpc":"2.0","id":"a","id":"b","method":"airc.capabilities"}`)
	status, body = rpcRawHTTP(t, f, malformed, f.token, "")
	if status != http.StatusOK {
		t.Fatalf("duplicate key status=%d body=%s", status, body)
	}
	var response rpcResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Error) == 0 {
		t.Fatalf("duplicate key response=%s err=%v", body, err)
	}
	for _, test := range []struct {
		id    string
		valid bool
	}{
		{"1e400", true},
		{"9007199254740993.5", true},
		{`"` + strings.Repeat("x", 257) + `"`, false},
	} {
		id := test.id
		payload := []byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"airc.capabilities"}`)
		status, body = rpcRawHTTP(t, f, payload, f.token, "")
		if status != http.StatusOK {
			t.Fatalf("invalid id %s status=%d body=%s", id, status, body)
		}
		response = decodeRPC(t, body)
		if test.valid {
			assertRPCID(t, response.ID, json.RawMessage(id))
		} else if len(response.Error) == 0 {
			t.Fatalf("invalid id %s lacked error: %s", id, body)
		}
	}
	oversized := []byte(`{"jsonrpc":"2.0","id":"large","method":"airc.capabilities","params":{"padding":"` + strings.Repeat("x", 64<<10) + `"}}`)
	status, body = rpcRawHTTP(t, f, oversized, f.token, "")
	response = decodeRPC(t, body)
	var sizeError struct {
		Code int `json:"code"`
	}
	if status != http.StatusOK || json.Unmarshal(response.Error, &sizeError) != nil || sizeError.Code != -32600 || string(response.ID) != "null" {
		t.Fatalf("oversized valid JSON status=%d body=%s", status, body)
	}
}
