package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPBodyBounds(t *testing.T) {
	tooLarge := strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","params":{"id":"` + strings.Repeat("x", mcpHTTPBodyLimit) + `"}}`)
	r := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", tooLarge)
	if _, _, err := mcpBody(r); err == nil {
		t.Fatal("oversized body accepted")
	}

	progress := `{"jsonrpc":"2.0","method":"tools/call","params":{"_meta":{"progressToken":"` + strings.Repeat("x", mcpFieldLimit+1) + `"}}}`
	r = httptest.NewRequest(http.MethodPost, "http://example.test/mcp", strings.NewReader(progress))
	if _, _, err := mcpBody(r); err == nil {
		t.Fatal("oversized progress token accepted")
	}
	for _, field := range []string{"method", "name", "id"} {
		body := `{"jsonrpc":"2.0","` + field + `":"` + strings.Repeat("\u2028", 200) + `"}`
		if err := validateMCPJSONBounds([]byte(body)); err == nil {
			t.Fatalf("escaped %s can inflate SDK error response", field)
		}
	}
}

func TestMCPPreflightHeadersAndOrigin(t *testing.T) {
	allowed := map[string]bool{"https://client.example": true}
	r := httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "https://client.example")
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	r.Header.Set("Access-Control-Request-Headers", "Content-Type, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
	w := httptest.NewRecorder()
	mcpOrigin(w, r, allowed)
	mcpPreflight(w, r, allowed)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://client.example" {
		t.Fatalf("preflight status=%d headers=%v", w.Code, w.Header())
	}

	r = httptest.NewRequest(http.MethodOptions, "http://example.test/mcp", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	w = httptest.NewRecorder()
	if mcpOrigin(w, r, allowed) {
		t.Fatal("foreign origin accepted")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status=%d", w.Code)
	}
}
