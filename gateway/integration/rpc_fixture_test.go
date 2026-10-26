//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func rpcHTTPSend(t *testing.T, f rpcHTTPFixture, path string, body []byte, http2 bool) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, f.token)
	request.Header.Set("Content-Type", "application/json")
	client := f.http1
	if http2 {
		client = f.http2
	}
	response, err := client.Do(request)
	if err != nil {
		alreadyDone := false
		select {
		case <-f.process.done:
			alreadyDone = true
		default:
		}
		if !alreadyDone {
			_ = f.process.command.Process.Signal(os.Interrupt)
			select {
			case <-f.process.done:
			case <-time.After(8 * time.Second):
			}
		}
		before := "running-before-signal"
		if alreadyDone {
			before = "already-exited"
		}
		state := before
		stderr := "not available before process joins"
		select {
		case <-f.process.done:
			state = fmt.Sprintf("%s; joined err=%v", before, f.process.err)
			stderr = f.process.stderr.String()
		default:
		}
		detail := string(body)
		if len(detail) > 512 {
			detail = detail[:512] + "..."
		}
		t.Fatalf("RPC HTTP request failed: %v; gateway=%s body=%s stderr=%s", err, state, detail, stderr)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func rpcRequestBody(t *testing.T, id any, method string, params any) []byte {
	t.Helper()
	value := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		value["id"] = id
	}
	if params != nil {
		value["params"] = params
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func decodeRPC(t *testing.T, data []byte) rpcResponse {
	t.Helper()
	var response rpcResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("invalid JSON-RPC response %q: %v", data, err)
	}
	if response.JSONRPC != "2.0" {
		t.Fatalf("wrong JSON-RPC version: %s", data)
	}
	// airc.request answers with the DetachedResult; tests compare its
	// operation-specific value.
	if len(response.Result) != 0 {
		response.Result = detachedPayload(response.Result)
	}
	return response
}

func assertRPCID(t *testing.T, got json.RawMessage, want any) {
	t.Helper()
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(expected) {
		t.Fatalf("JSON-RPC id=%s want=%s", got, expected)
	}
}

// rpcRequestParams is request as airc.request params naming the fixture
// author.
func rpcRequestParams(request core.Request) map[string]any {
	data, _ := json.Marshal(request)
	params := map[string]any{}
	_ = json.Unmarshal(data, &params)
	params["author"] = map[string]string{"nick": fixtureAuthor, "token": fixtureAccount}
	return params
}
