//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/coder/websocket"
)

func TestWebSocketCLIUpgradeAndEnvelopeBoundaries(t *testing.T) {
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
	for _, bad := range []*tls.Config{
		{MinVersion: tls.VersionTLS13, RootCAs: x509.NewCertPool()},
		{MinVersion: tls.VersionTLS13, RootCAs: f.certificate.Client.RootCAs, ServerName: "wrong.example"},
	} {
		transport := &http.Transport{TLSClientConfig: bad}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		conn, _, err := websocket.Dial(ctx, f.base, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{adapter.WebSocketSubprotocol}})
		cancel()
		transport.CloseIdleConnections()
		if err == nil {
			_ = conn.CloseNow()
			t.Fatal("WebSocket TLS accepted wrong trust/name")
		}
	}
	allowed := websocketDial(t, f, http.Header{"Origin": []string{"https://allowed.example"}, "Cookie": []string{"session=untrusted"}})
	defer allowed.CloseNow()
	r := core.Request{Version: 1, RequestID: "discovery", Operation: core.CapabilitiesGet}
	websocketWrite(t, allowed, adapter.FrameRequest{Version: 1, CorrelationID: "cookie-alone", Profile: "signed", Envelope: "invalid"})
	if response := websocketRead(t, allowed); response.Error == nil || response.Error.Code != core.AuthenticationFailed {
		t.Fatal("ambient cookie became identity authority", response)
	}
	for _, profile := range []string{"signed", "encrypted"} {
		token := f.credentials.token(t, r, profile == "encrypted")
		parts := strings.Split(token, ".")
		segment := len(parts) - 1
		if profile == "encrypted" {
			segment = 3
		}
		bytes, err := base64.RawURLEncoding.DecodeString(parts[segment])
		if err != nil {
			t.Fatal(err)
		}
		bytes[0] ^= 1
		parts[segment] = base64.RawURLEncoding.EncodeToString(bytes)
		websocketWrite(t, allowed, adapter.FrameRequest{Version: 1, CorrelationID: "tamper-" + profile, Profile: profile, Envelope: strings.Join(parts, ".")})
		if response := websocketRead(t, allowed); response.Error == nil || response.Error.Code != core.AuthenticationFailed {
			t.Fatal("tampered envelope accepted", response)
		}
	}
	expired := f.credentials.validityToken(t, r, time.Now().Unix()-100, time.Now().Unix()-70, false)
	websocketWrite(t, allowed, adapter.FrameRequest{Version: 1, CorrelationID: "expired", Profile: "signed", Envelope: expired})
	if response := websocketRead(t, allowed); response.Error == nil || response.Error.Code != core.AuthenticationFailed {
		t.Fatal("expired envelope accepted", response)
	}
	websocketWrite(t, allowed, adapter.FrameRequest{Version: 1, CorrelationID: "valid", Profile: "signed", Envelope: f.credentials.token(t, r, false)})
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
		{websocket.MessageText, `{"version":1,"version":1,"correlation_id":"bad","profile":"encrypted","envelope":"invalid"}`},
		{websocket.MessageText, `{"version":1,"correlation_id":"bad","profile":"encrypted","envelope":"invalid","cursor":"unsigned"}`},
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
	websocketWrite(t, f.conn, adapter.FrameRequest{Version: 1, CorrelationID: "wrong-profile", Profile: "signed", Envelope: f.credentials.token(t, discovery, false)})
	if response := websocketRead(t, f.conn); response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatal("plaintext WebSocket accepted signature only", response)
	}
	watch := core.Request{Version: 1, RequestID: "watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1}
	websocketWrite(t, f.conn, adapter.FrameRequest{Version: 1, CorrelationID: "active", Profile: "encrypted", Envelope: f.credentials.token(t, watch, true), Stream: true})
	if response := websocketRead(t, f.conn); response.Page == nil {
		t.Fatal("watch did not start", response)
	}
	websocketWrite(t, f.conn, adapter.FrameRequest{Version: 1, CorrelationID: "active", Profile: "signed", Envelope: f.credentials.token(t, discovery, false)})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, _, err := f.conn.Read(ctx); err == nil {
		t.Fatal("active handle collision received ambiguous response")
	}
	f.stop()
}
