package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPChannelsRejectsQuery(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/channels?limit=1", nil)
	response := httptest.NewRecorder()
	httpChannels(response, request, nil, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestCORSRouteChannelsRead(t *testing.T) {
	if !corsRoute("/v1/channels", http.MethodGet) {
		t.Fatal("channels GET missing CORS route")
	}
	if corsRoute("/v1/channels", http.MethodPost) {
		t.Fatal("channels POST unexpectedly allowed by CORS route")
	}
}
