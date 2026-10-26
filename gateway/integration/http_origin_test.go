package gateway_test

import (
	"net/http"
	"testing"
)

func TestHTTPExactOriginsPreflightAndAuthorHeaders(t *testing.T) {
	t.Parallel()
	const origin = "https://fixture.example"
	f := gatewayHTTPOriginFixture(t, true, []string{origin}, false)
	for _, test := range []struct {
		method, path, origin, preflight, headers, token string
		status                                          int
	}{
		{"OPTIONS", "/v1/events?kind=room&target=%23gateway-fixture", origin, "GET", "airc-nick, last-event-id", "", 204},
		{"OPTIONS", "/v1/messages", origin, "POST", "airc-nick, content-type, idempotency-key", "", 204},
		{"OPTIONS", "/v1/requests/put-id", origin, "PUT", "airc-nick, content-type, idempotency-key", "", 204},
		{"OPTIONS", "/v1/requests/put-id/extra", origin, "PUT", "airc-nick", "", 403},
		{"OPTIONS", "/v1/events", origin, "POST", "airc-nick", "", 403},
		{"OPTIONS", "/v1/unknown", origin, "GET", "airc-nick", "", 403},
		{"OPTIONS", "/v1/events", origin, "GET", "x-forged-author", "", 403},
		{"OPTIONS", "/v1/events", origin, "GET", "airc-nick, airc-nick", "", 403},
		{"OPTIONS", "/v1/events", "https://evil.example", "GET", "airc-nick", "", 403},
		{"GET", "/v1/capabilities", origin, "", "", "", 200},
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
			setAuthorHeaders(req, test.token)
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
