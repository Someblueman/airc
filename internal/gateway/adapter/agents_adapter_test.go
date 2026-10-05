package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAgentsRejectsUnknownOrDuplicateQuery(t *testing.T) {
	for _, path := range []string{"/v1/agents?target=%23room", "/v1/agents?limit=1&limit=2"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		httpAgents(w, r, nil, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%q", path, w.Code, w.Body.String())
		}
	}
}

func TestCORSRouteAgentsRead(t *testing.T) {
	if !corsRoute("/v1/agents", http.MethodGet) {
		t.Fatal("agents GET missing CORS route")
	}
	if corsRoute("/v1/agents", http.MethodPost) {
		t.Fatal("agents POST unexpectedly allowed by CORS route")
	}
}
