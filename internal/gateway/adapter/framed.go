package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const FramedALPN = "airc-gateway/1"
const MaxRequestFrameBytes = 16 << 10
const MaxResponseFrameBytes = 64 << 10

// CorrelationID is a connection-local handle, never a durable request ID or cursor.
type FrameRequest struct {
	Version       int    `json:"version"`
	CorrelationID string `json:"correlation_id"`
	Profile       string `json:"profile"`
	Envelope      string `json:"envelope"`
	Stream        bool   `json:"stream,omitempty"`
}

type FrameResponse struct {
	Version       int                      `json:"version"`
	CorrelationID string                   `json:"correlation_id"`
	Event         string                   `json:"event"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Page          *backend.HistoryPage     `json:"page,omitempty"`
	Reason        string                   `json:"reason,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`
}

// ServeFramed owns and joins every accepted connection and request. The caller
// bounds accepted connections; requests share a separate global admission cap.
// Plaintext is local-only (enforced by config) and accepts encrypted ingress.
// Responses are clear on that local transport; remote use requires TLS.
func ServeFramed(ctx context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}, encryptedOnly bool) error {
	if d == nil || cap(requests) < 1 {
		return errors.New("invalid framed dispatcher or request capacity")
	}
	ctx, cancel := context.WithCancel(ctx)
	var connections sync.WaitGroup
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		connections.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		connections.Go(func() { serveFramedConnection(ctx, conn, d, requests, encryptedOnly) })
	}
}

func serveFramedConnection(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, encryptedOnly bool) {
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		_ = conn.Close()
	}()
	if secure, ok := conn.(*tls.Conn); ok {
		handshake, finish := context.WithTimeout(ctx, 3*time.Second)
		err := secure.HandshakeContext(handshake)
		finish()
		if err != nil || secure.ConnectionState().NegotiatedProtocol != FramedALPN {
			return
		}
	}
	var writes sync.Mutex
	write := func(response FrameResponse) error {
		body, err := json.Marshal(response)
		if err != nil || len(body) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		writes.Lock()
		defer writes.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err == nil {
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(len(body)))
			_, err = io.Copy(conn, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(body)))
		}
		if err != nil {
			cancel()
		}
		return err
	}
	first := true
	next := func() ([]byte, error) {
		idle := 30 * time.Second
		if first {
			idle = 5 * time.Second
		}
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:1]); err != nil {
			return nil, err
		}
		first = false
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(conn, header[1:]); err != nil {
			return nil, err
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > MaxRequestFrameBytes {
			return nil, store.ErrInvalid
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	runCorrelated(ctx, correlatedTransport{next: next, write: write, watchModes: []string{"long_poll", "framed_stream"}}, d, requests, encryptedOnly)
}

func correlationID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
