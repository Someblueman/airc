//go:build darwin || linux

package gateway_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/coder/websocket"
)

func TestWebSocketCLIUpgradeAndFrameBoundaries(t *testing.T) {
	f := websocketGatewayFixture(t, true, "127.0.0.1:0", []string{"https://allowed.example"})
	for _, headers := range []http.Header{
		{"Origin": []string{"https://wrong.example"}},
		{"Origin": []string{"null"}},
		{"Origin": []string{"https://allowed.example", "https://wrong.example"}},
		{"Authorization": []string{"Bearer untrusted"}},
	} {
		transport := &http.Transport{TLSClientConfig: f.certificate.Client.Clone()}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		conn, response, err := websocket.Dial(ctx, f.base, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: headers, Subprotocols: []string{adapter.WebSocketSubprotocol}})
		cancel()
		transport.CloseIdleConnections()
		if err == nil {
			_ = conn.CloseNow()
			t.Fatal("unauthorized upgrade succeeded", headers)
		}
		if response == nil || response.StatusCode < 400 || response.StatusCode >= 500 {
			t.Fatal("upgrade not rejected by policy", response, err)
		}
	}
	allowed := websocketDial(t, f, http.Header{"Origin": []string{"https://allowed.example"}, "Cookie": []string{"session=untrusted"}})
	defer allowed.CloseNow()
	r := core.Request{Version: 1, RequestID: "discovery", Operation: core.CapabilitiesGet}
	websocketWrite(t, allowed, frameRequest{Version: 1, CorrelationID: "cookie-alone", Profile: "plain", Request: "{}"})
	if response := websocketRead(t, allowed); response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatal("ambient cookie became an author", response)
	}
	websocketWrite(t, allowed, frameRequest{Version: 1, CorrelationID: "valid", Profile: "plain", Request: f.credentials.token(t, r, false)})
	if response := websocketRead(t, allowed); response.Result == nil || response.Result.Capabilities == nil {
		t.Fatal("allowed origin damaged after rejections", response)
	}
	f.stop()
}

func TestWebSocketCLIMalformedMessagesAndDuplicateHandle(t *testing.T) {
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", nil)
	for _, message := range []struct {
		kind websocket.MessageType
		body string
	}{
		{websocket.MessageBinary, "{}"},
		{websocket.MessageText, strings.Repeat("x", adapter.MaxRequestFrameBytes+1)},
		{websocket.MessageText, `{"version":1,"version":1,"correlation_id":"bad","request":{}}`},
		{websocket.MessageText, `{"version":1,"correlation_id":"bad","request":{},"cursor":"unbound"}`},
	} {
		conn := websocketDial(t, f, nil)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		err := conn.Write(ctx, message.kind, []byte(message.body))
		if err == nil {
			_, _, err = conn.Read(ctx)
		}
		cancel()
		_ = conn.CloseNow()
		if err == nil {
			t.Fatal("malformed message received response")
		}
	}
	discovery := core.Request{Version: 1, RequestID: "discovery", Operation: core.CapabilitiesGet}
	websocketWrite(t, f.conn, frameRequest{Version: 1, CorrelationID: "wrong-profile", Profile: "unknown", Request: f.credentials.token(t, discovery, false)})
	if response := websocketRead(t, f.conn); response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatal("WebSocket accepted an unknown profile", response)
	}
	watch := core.Request{Version: 1, RequestID: "watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
	websocketWrite(t, f.conn, frameRequest{Version: 1, CorrelationID: "active", Profile: "plain", Request: f.credentials.token(t, watch, true), Stream: true})
	if response := websocketRead(t, f.conn); response.Page == nil {
		t.Fatal("watch did not start", response)
	}
	websocketWrite(t, f.conn, frameRequest{Version: 1, CorrelationID: "active", Profile: "plain", Request: f.credentials.token(t, discovery, false)})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, _, err := f.conn.Read(ctx); err == nil {
		t.Fatal("active handle collision received ambiguous response")
	}
	f.stop()
}
