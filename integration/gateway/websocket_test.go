//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebSocketCLISharedOperationsAndMultiplexedWatch(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			f := websocketGatewayFixture(t, secure, "127.0.0.1:0", nil)
			duplexOperations(t, f.credentials, secure, "websocket", func(r adapter.FrameRequest) { websocketWrite(t, f.conn, r) }, func() adapter.FrameResponse { return websocketRead(t, f.conn) }, f.stop)
		})
	}
}

func TestWebSocketCLINativeIPv6(t *testing.T) {
	for _, secure := range []bool{false, true} {
		f := websocketGatewayFixture(t, secure, "[::1]:0", nil)
		profile := "encrypted"
		if secure {
			profile = "signed"
		}
		websocketWrite(t, f.conn, adapter.FrameRequest{Version: 1, CorrelationID: "ipv6", Profile: profile, Envelope: f.credentials.token(t, core.Request{Version: 1, RequestID: "ipv6", Operation: core.CapabilitiesGet}, !secure)})
		if r := websocketRead(t, f.conn); r.Result == nil || r.Result.Capabilities == nil {
			t.Fatal("native IPv6 WebSocket failed", r)
		}
		f.stop()
	}
}
