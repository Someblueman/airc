package gateway_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestHTTPDetachedEventsDenials(t *testing.T) {
	f := httpGatewayFixture(t, true, nil, false, true)
	r := core.Request{Version: 1, RequestID: "must-not-admit", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "not a watch"}
	for _, profile := range []string{"signed", "encrypted"} {
		encrypted := profile == "encrypted"
		token := f.credentials.token(t, r, encrypted)
		for _, test := range []struct {
			body, header, query string
			status              int
		}{
			{token, "", "", 400},
			{token, "Last-Event-ID", "", 400},
			{token, "Authorization", "", 400},
			{token, "Idempotency-Key", "", 400},
			{token + "x", "", "", 401},
			{token, "", "?start=retained", 403},
			{strings.Repeat("x", 65537), "", "", 413},
		} {
			req, _ := http.NewRequest(http.MethodPost, f.server.URL+"/v1/envelopes/"+profile+"/events"+test.query, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/jose")
			if test.header != "" {
				req.Header.Set(test.header, "unsigned")
			}
			response, err := f.server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatal("detached stream denial failed", profile, test.header, response.StatusCode, test.status)
			}
			if test.status == 401 {
				scheme := "AIRC-JWS"
				if encrypted {
					scheme = "AIRC-JWE"
				}
				if response.Header.Get("WWW-Authenticate") != scheme+` realm="airc-gateway"` {
					t.Fatal("wrong challenge", response.Header)
				}
			}
		}
	}
	if _, err := f.journal.Lookup(t.Context(), f.identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("stream endpoint admitted mutation", err)
	}
}
