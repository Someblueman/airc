//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/coder/websocket"
)

type websocketFixture struct {
	conn         *websocket.Conn
	credentials  *detachedCredentials
	socket, base string
	stateDir     string
	certificate  testtls.Certificate
	stop         func()
}

func websocketGatewayFixture(t *testing.T, secure bool, address string, origins []string) websocketFixture {
	t.Helper()
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, account)
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 2, 1
	certificate := testtls.New(t)
	listener := config.Listener{ID: "websocket", Protocol: "websocket", Enabled: true, Address: address, AllowedOrigins: origins, Operations: dispatch.ImplementedOperations()}
	scheme := "ws"
	if secure {
		listener.TLSCertFile, listener.TLSKeyFile = certificate.CertPath, certificate.KeyPath
		scheme = "wss"
	}
	c.Listeners = []config.Listener{listener}
	p := startGateway(t, gatewayBinary(t), c)
	f := websocketFixture{credentials: credentials, socket: socket, stateDir: c.StateDir, base: scheme + "://" + gatewayAddress(t, p) + "/v1/socket", certificate: certificate}
	f.conn = websocketDial(t, f, nil)
	t.Cleanup(func() { _ = f.conn.CloseNow() })
	f.stop = func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("WebSocket CLI did not join hijacked connections")
		}
	}
	return f
}

func websocketDial(t *testing.T, f websocketFixture, headers http.Header) *websocket.Conn {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: f.certificate.Client.Clone()}
	t.Cleanup(transport.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, f.base, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, HTTPHeader: headers, Subprotocols: []string{adapter.WebSocketSubprotocol}})
	if err != nil {
		if response != nil {
			t.Fatal("WebSocket upgrade", response.Status, err)
		}
		t.Fatal(err)
	}
	if conn.Subprotocol() != adapter.WebSocketSubprotocol {
		t.Fatal("unnegotiated WebSocket protocol")
	}
	conn.SetReadLimit(adapter.MaxResponseFrameBytes)
	return conn
}

func websocketWrite(t *testing.T, conn *websocket.Conn, request adapter.FrameRequest) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err = conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
}

func websocketRead(t *testing.T, conn *websocket.Conn) adapter.FrameResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	kind, body, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText {
		t.Fatal("WebSocket frame", kind, err)
	}
	var response adapter.FrameResponse
	if json.Unmarshal(body, &response) != nil || response.Version != 1 || response.CorrelationID == "" {
		t.Fatal("invalid WebSocket result")
	}
	return response
}
