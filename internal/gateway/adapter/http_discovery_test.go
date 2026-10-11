package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestDiscoveryRequestRejectsUnknownOrDuplicateQuery(t *testing.T) {
	for _, path := range []string{"/v1/agents?target=%23room", "/v1/agents?limit=1&limit=2", "/v1/channels?limit=1", "/v1/directory?nick="} {
		_, ok, err := discoveryRequest(httptest.NewRequest(http.MethodGet, path, nil))
		if !ok || err == nil {
			t.Fatalf("%s ok=%v err=%v", path, ok, err)
		}
	}
}

func TestDiscoveryRequestBuildsReads(t *testing.T) {
	for path, operation := range map[string]core.Operation{
		"/v1/agents?limit=2":         core.AgentsRead,
		"/v1/channels":               core.ChannelsRead,
		"/v1/directory?nick=fixture": core.Directory,
	} {
		request, ok, err := discoveryRequest(httptest.NewRequest(http.MethodGet, path, nil))
		if !ok || err != nil || request.Operation != operation {
			t.Fatalf("%s request=%+v ok=%v err=%v", path, request, ok, err)
		}
	}
	if _, ok, _ := discoveryRequest(httptest.NewRequest(http.MethodGet, "/v1/requests", nil)); ok {
		t.Fatal("non-discovery path accepted")
	}
}

func TestCORSRouteDiscoveryReads(t *testing.T) {
	for _, path := range []string{"/v1/agents", "/v1/channels"} {
		if !corsRoute(path, http.MethodGet) {
			t.Fatalf("%s GET missing CORS route", path)
		}
		if corsRoute(path, http.MethodPost) {
			t.Fatalf("%s POST unexpectedly allowed by CORS route", path)
		}
	}
}
