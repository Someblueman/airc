package gateway_test

import (
	"net/http"
	"testing"
)

func TestHTTPExactOriginsPreflightAndBearerAuthority(t *testing.T) {
	t.Parallel()
	const origin = "https://fixture.example"
	f := gatewayHTTPOriginFixture(t, true, []string{origin}, false)
	for _, test := range []struct {
		method, path, origin, preflight, headers, token string
		status                                          int
	}{
		{"OPTIONS", "/v1/events?kind=room&target=%23gateway-fixture", origin, "GET", "authorization, last-event-id", "", 204},
		{"OPTIONS", "/v1/messages", origin, "POST", "authorization, content-type, idempotency-key", "", 204},
		{"OPTIONS", "/v1/envelopes/signed", origin, "POST", "content-type", "", 204},
		{"OPTIONS", "/v1/envelopes/encrypted", origin, "POST", "content-type", "", 204},
		{"OPTIONS", "/v1/envelopes/signed/events", origin, "POST", "content-type", "", 204},
		{"OPTIONS", "/v1/envelopes/encrypted/events", origin, "POST", "content-type", "", 204},
		{"OPTIONS", "/v1/requests/put-id", origin, "PUT", "authorization, content-type, idempotency-key", "", 204},
		{"OPTIONS", "/v1/requests/put-id/extra", origin, "PUT", "authorization", "", 403},
		{"OPTIONS", "/v1/events", origin, "POST", "authorization", "", 403},
		{"OPTIONS", "/v1/unknown", origin, "GET", "authorization", "", 403},
		{"OPTIONS", "/v1/events", origin, "GET", "x-forged-principal", "", 403},
		{"OPTIONS", "/v1/events", origin, "GET", "authorization, authorization", "", 403},
		{"OPTIONS", "/v1/events", "https://evil.example", "GET", "authorization", "", 403},
		{"GET", "/v1/capabilities", origin, "", "", "", 401},
		{"GET", "/v1/capabilities", origin, "", "", f.token, 200},
		{"GET", "/v1/capabilities", origin + ".evil", "", "", f.token, 403},
		{"GET", "/v1/capabilities", "null", "", "", f.token, 403},
	} {
		req, _ := http.NewRequest(test.method, f.server.URL+test.path, nil)
		req.Header.Set("Origin", test.origin)
		if test.preflight != "" {
			req.Header.Set("Access-Control-Request-Method", test.preflight)
			req.Header.Set("Access-Control-Request-Headers", test.headers)
		}
		if test.token != "" {
			req.Header.Set("Authorization", "Bearer "+test.token)
		}
		req.Header.Set("Cookie", "session=forged")
		response, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatal(test, response.Status)
		}
		if response.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatal("cookie authority enabled")
		}
		if test.origin == origin && response.Header.Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("authorized browser cannot read typed result", test, response.Header)
		}
		if test.origin != origin && response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unpermitted origin granted visibility")
		}
	}
}
