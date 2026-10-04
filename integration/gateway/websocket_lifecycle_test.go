//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWebSocketCLIPreauthAndMissingPongDeadlines(t *testing.T) {
	f := websocketGatewayFixture(t, false, "127.0.0.1:0", nil)
	request := core.Request{Version: 1, RequestID: "heartbeat", Operation: core.CapabilitiesGet}
	websocketWrite(t, f.conn, adapter.FrameRequest{Version: 1, CorrelationID: "heartbeat", Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	// Do not read this client: coder responds to pings only while a reader runs.
	// The server must close it when its ten-second ping has no pong for three seconds.
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
		if err == nil || errors.Is(err, context.DeadlineExceeded) || time.Since(began) > 6*time.Second {
			t.Fatal("preauth application deadline escaped", err, time.Since(began))
		}
	case <-time.After(8 * time.Second):
		t.Fatal("preauth connection not closed")
	}
	remaining := 14*time.Second - time.Since(began)
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
