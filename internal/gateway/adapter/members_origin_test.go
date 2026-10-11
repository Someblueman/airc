package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadSnapshotBrowserPreflightRequiresExactOriginAndReadMethod(t *testing.T) {
	allowed := map[string]bool{"https://members.example": true}
	for _, path := range []string{"/v1/members?kind=room&target=%23room", "/v1/channels", "/v1/live", "/v1/live/events"} {
		t.Run(path, func(t *testing.T) {
			for _, tc := range []struct {
				name, origin, method string
				status               int
			}{
				{"allowed", "https://members.example", http.MethodGet, http.StatusNoContent},
				{"foreign origin", "https://foreign.example", http.MethodGet, http.StatusForbidden},
				{"missing origin", "", http.MethodGet, http.StatusForbidden},
				{"mutation method", "https://members.example", http.MethodPost, http.StatusForbidden},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := httptest.NewRequest(http.MethodOptions, path, nil)
					if tc.origin != "" {
						r.Header.Set("Origin", tc.origin)
					}
					r.Header.Set("Access-Control-Request-Method", tc.method)
					r.Header.Set("Access-Control-Request-Headers", "AIRC-Nick")
					w := httptest.NewRecorder()
					if httpOrigin(w, r, allowed) {
						t.Fatal("preflight entered authenticated data handler")
					}
					if w.Code != tc.status {
						t.Fatal("preflight status", w.Code, w.Body.String())
					}
					if w.Header().Get("Access-Control-Allow-Credentials") != "" {
						t.Fatal("cookie credentials enabled")
					}
					if tc.status == http.StatusNoContent && w.Header().Get("Access-Control-Allow-Methods") != http.MethodGet {
						t.Fatal("missing read method grant")
					}
				})
			}
		})
	}
}
