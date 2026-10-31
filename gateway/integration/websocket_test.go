//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testnet"
)

func TestWebSocketCLINativeIPv6(t *testing.T) {
	t.Parallel()
	testnet.RequireIPv6Loopback(t)
	for _, secure := range []bool{false, true} {
		f := websocketGatewayFixture(t, secure, "[::1]:0", nil)
		profile := "encrypted"
		if secure {
			profile = "signed"
		}
		websocketWrite(t, f.conn, frameRequest{Version: 1, CorrelationID: "ipv6", Profile: profile, Request: f.credentials.token(t, core.Request{Version: 1, RequestID: "ipv6", Operation: core.CapabilitiesGet}, !secure)})
		if r := websocketRead(t, f.conn); r.Result == nil || r.Result.Capabilities == nil {
			t.Fatal("native IPv6 WebSocket failed", r)
		}
		f.stop()
	}
}
