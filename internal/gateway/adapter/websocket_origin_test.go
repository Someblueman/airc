package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/coder/websocket"
)

func TestWebSocketOriginPolicySnapshotsCallerSlice(t *testing.T) {
	origins := []string{"https://allowed.example"}
	// Upgrade itself has no identity authority and must never dispatch a request.
	configured, err := WebSocketServer(new(dispatch.Dispatcher), make(chan struct{}, 1), origins, Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	origins[0] = "https://changed.example"
	server := httptest.NewServer(configured.Handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/socket", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://allowed.example"}}, Subprotocols: []string{WebSocketSubprotocol}})
	if err != nil {
		if response != nil {
			t.Fatal("caller mutation changed origin policy", response.Status, err)
		}
		t.Fatal(err)
	}
	_ = conn.CloseNow()
}
