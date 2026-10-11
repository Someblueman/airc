//go:build darwin || linux

package adapter

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// The pipe exercises application admission and cancellation under the host race
// detector. Native Linux socket and gateway tests separately prove SCTP records.
func sctpApplicationFixture(t *testing.T) (plainFixture, sctpFrame) {
	t.Helper()
	f := newPlainFixture(t, config.Listener{ID: "sctp", Protocol: "sctp", Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}})
	return f, sctpFrame{Version: 1, CorrelationID: "sctp-request", Profile: "plain", Request: json.RawMessage(f.body(t, mqttSNRequest()))}
}

// sctpFrame is a plain correlated request frame as a client writes it.
type sctpFrame struct {
	Version       int             `json:"version"`
	CorrelationID string          `json:"correlation_id"`
	Profile       string          `json:"profile,omitempty"`
	Request       json.RawMessage `json:"request,omitempty"`
}

func startSCTPApplication(t *testing.T, f plainFixture, request sctpFrame, slots chan struct{}, writing chan struct{}, mode SCTPMode) (net.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	var conn net.Conn = server
	if writing != nil {
		conn = &sctpObservedWriter{Conn: server, writing: writing}
	}
	go func() {
		defer close(finished)
		done <- serveSCTPConnection(ctx, conn, f.d, slots, mode)
	}()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("SCTP test connection did not join")
		}
	})
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(body); err != nil {
		t.Fatal(err)
	}
	return client, cancel, done
}

type sctpObservedWriter struct {
	net.Conn
	writing chan struct{}
}

func (c *sctpObservedWriter) Write(body []byte) (int, error) {
	close(c.writing)
	return c.Conn.Write(body)
}
