//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestChannelsReadOverSignedTLSFramed(t *testing.T) {
	conn, credentials, stop := framedFixture(t, true)
	defer conn.Close()
	defer stop()
	request := core.Request{Version: 1, RequestID: "channels-tls-framed", Operation: core.ChannelsRead}
	status, result := contextFramedCall(t, conn, credentials.token(t, request, false), request.RequestID, false)
	// This native fixture provisions an ephemeral account and creates no rooms.
	if status != http.StatusOK || result.Channels == nil || result.Channels.Channels == nil || len(result.Channels.Channels) != 0 {
		t.Fatalf("signed TLS framed empty snapshot=%+v status=%d", result.Channels, status)
	}
}

func TestChannelsReadOverSignedSecureWebSocket(t *testing.T) {
	f := websocketGatewayFixture(t, true, "127.0.0.1:0", nil)
	defer f.conn.CloseNow()
	defer f.stop()
	seedChannels(t, f.socket)
	want := nativeChannels(t, f.socket)
	request := core.Request{Version: 1, RequestID: "channels-wss", Operation: core.ChannelsRead}
	status, result := contextWebSocketCall(t, f.conn, f.credentials.token(t, request, false), request.RequestID, false)
	if status != http.StatusOK || result.Channels == nil || !channelsEqual(result.Channels.Channels, want) {
		t.Fatalf("signed WSS snapshot=%+v native=%+v status=%d", result.Channels, want, status)
	}
}
