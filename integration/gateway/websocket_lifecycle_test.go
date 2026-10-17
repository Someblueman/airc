//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/coder/websocket"
)

func TestWebSocketCLIPreauthAndMissingPongDeadlines(t *testing.T) {
	ping := shortGatewayTimer(t, websocketPingTimer)
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", nil)
	request := core.Request{Version: 1, RequestID: "heartbeat", Operation: core.CapabilitiesGet}
	websocketWrite(t, f.conn, frameRequest{Version: 1, CorrelationID: "heartbeat", Profile: "plain", Request: f.credentials.token(t, request, true)})
	// Do not read this client: coder responds to pings only while a reader runs.
	// The server must close it when its ping has no pong by the next ping.
	began := time.Now()
	noAuth := websocketDial(t, f, nil)
	defer noAuth.CloseNow()
	preauth := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
		defer cancel()
		_, _, err := noAuth.Read(ctx)
		preauth <- err
	}()
	select {
	case err := <-preauth:
		if err == nil || errors.Is(err, context.DeadlineExceeded) || time.Since(began) < 4*time.Second || time.Since(began) > 6*time.Second {
			t.Fatal("preauth application deadline escaped", err, time.Since(began))
		}
	case <-time.After(8 * time.Second):
		t.Fatal("preauth connection not closed")
	}
	rpcNoJobs(t, f.stateDir)
	remaining := ping + 4*time.Second - time.Since(began)
	if remaining > 0 {
		timer := time.NewTimer(remaining)
		select {
		case <-timer.C:
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test canceled")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var err error
	for n := 0; n < 4; n++ {
		_, _, err = f.conn.Read(ctx)
		if err != nil {
			break
		}
	}
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("missing pong left connection alive", err)
	}
	f.stop()
}

// A listener's keepalive sets its ping interval, with a pong due by the
// next ping, and how long a socket may wait between requests.
func TestWebSocketListenerKeepalive(t *testing.T) {
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", nil, func(l *config.Listener) {
		l.Keepalive = &config.Keepalive{IdleSeconds: 5, PingSeconds: 1}
	})
	request := func(conn *websocket.Conn, id string) {
		r := core.Request{Version: 1, RequestID: id, Operation: core.CapabilitiesGet}
		websocketWrite(t, conn, frameRequest{Version: 1, CorrelationID: id, Profile: "plain", Request: f.credentials.token(t, r, true)})
	}
	// closed reads conn until it fails, reporting when.
	closed := func(conn *websocket.Conn, within time.Duration) time.Time {
		ctx, cancel := context.WithTimeout(t.Context(), within)
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("socket still open after", within)
				}
				return time.Now()
			}
		}
	}

	// A client that answers pings but sends nothing more is closed after
	// the idle timeout, not the default thirty seconds.
	request(f.conn, "idle")
	sent := time.Now()
	if waited := closed(f.conn, 12*time.Second).Sub(sent); waited < 4*time.Second {
		t.Fatal("closed before the idle timeout", waited)
	}

	// A client that stops reading never answers a ping: closed by the
	// second ping, well inside the idle timeout.
	quiet := websocketDial(t, f, nil)
	defer quiet.CloseNow()
	request(quiet, "deaf")
	sent = time.Now()
	time.Sleep(3500 * time.Millisecond)
	if waited := closed(quiet, 3*time.Second).Sub(sent); waited > 5*time.Second {
		t.Fatal("an unanswered ping left the socket open", waited)
	}
}
