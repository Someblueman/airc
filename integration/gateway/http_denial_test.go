package gateway_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestHTTPDenialsAndScopedLookup(t *testing.T) {
	t.Parallel()
	f := gatewayHTTPFixture(t, true)
	r := core.Request{Version: 1, RequestID: "owned", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "owned post"}
	valid := requestJSON(t, r)
	status, _, _ := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, valid)
	if status != 202 {
		t.Fatal("owner admission failed", status)
	}
	status, _, _ = httpCall(t, f, http.MethodGet, "/v1/requests/owned", f.other, nil)
	if status != 404 {
		t.Fatal("cross-principal result exposed", status)
	}
	r.Target.Name = "#forbidden"
	for _, tc := range []struct {
		name, method, path, token string
		body                      []byte
		status                    int
	}{
		{"credential", http.MethodPost, "/v1/messages", strings.Repeat("f", 64), valid, 401},
		{"permission", http.MethodPost, "/v1/messages", f.token, requestJSON(t, r), 403},
		{"oversized", http.MethodPost, "/v1/messages", f.token, bytes.Repeat([]byte("x"), core.MaxEnvelopeBytes+1), 413},
		{"strict", http.MethodPost, "/v1/messages", f.token, []byte(`{"version":1,"request_id":"injected","operation":"send","account":"other"}`), 400},
		{"read-post", http.MethodPost, "/v1/messages", f.token, requestJSON(t, core.Request{Version: 1, RequestID: "query", Operation: core.ReceiptGet, LookupID: "owned"}), 400},
		{"safe-get", http.MethodGet, "/v1/messages", f.token, valid, 404},
		{"URL-secret", http.MethodGet, "/v1/requests/owned?token=secret", f.token, nil, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _, data := httpCall(t, f, tc.method, tc.path, tc.token, tc.body)
			if status != tc.status {
				t.Fatal("incorrect denial", status, string(data))
			}
		})
	}
	for _, mode := range []string{"origin", "duplicate-auth", "conflicting-id", "missing-auth"} {
		r, err := http.NewRequest(http.MethodPost, f.server.URL+"/v1/messages", bytes.NewReader(valid))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("Content-Type", "application/json")
		want := 403
		switch mode {
		case "missing-auth":
			r.Header.Del("Authorization")
			want = 401
		case "origin":
			r.Header.Set("Origin", "https://unconfigured.example")
		case "duplicate-auth":
			r.Header.Add("Authorization", "Bearer "+f.other)
		case "conflicting-id":
			r.Header.Set("Idempotency-Key", "different")
			want = 409
		}
		response, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if want == 401 && response.Header.Get("WWW-Authenticate") == "" {
			t.Fatal("authentication challenge missing")
		}
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Fatal(mode, response.StatusCode)
		}
	}
}
