//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func http3DetachedCall(t *testing.T, f http3GatewayFixture, token string, encrypted bool) (int, dispatch.DetachedResult) {
	t.Helper()
	path := "/v1/envelopes/signed"
	if encrypted {
		path = "/v1/envelopes/encrypted"
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+f.address+path, bytes.NewBufferString(token))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/jose")
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	assertHTTP3Security(t, response)
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	var result dispatch.DetachedResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("HTTP/3 detached response=%s err=%v", data, err)
	}
	return response.StatusCode, result
}

func TestHTTP3SignedAndEncryptedProfiles(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	for _, encrypted := range []bool{false, true} {
		r := core.Request{Version: 1, RequestID: "http3-profile-" + map[bool]string{false: "signed", true: "encrypted"}[encrypted], Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "http3 detached 🙂"}
		status, result := http3DetachedCall(t, f, f.credentials.token(t, r, encrypted), encrypted)
		if status != http.StatusAccepted || result.Result == nil || !result.Result.Durable {
			t.Fatalf("HTTP/3 profile=%t status/result=%d/%+v", encrypted, status, result)
		}
	}
}
