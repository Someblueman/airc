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

// FramedStream is the deadline-capable byte stream used by the framed
// protocol. net.Conn satisfies this interface; service-owned pipe carriers
// can implement it without exposing transport-specific details here.
type FramedStream interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

var errFramedCleanEOF = errors.New("clean framed stream eof")

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
	Sequence      uint64                   `json:"sequence,omitempty"`
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
	_ = serveFramedStreamTransport(parent, conn, d, requests, encryptedOnly, true, false, []string{"long_poll", "framed_stream"}, 3*time.Second)
}

// ServeFramedStream serves one already-connected framed stream and owns its
// lifetime. A clean EOF between frames drains admitted requests before the
// stream is cancelled; a partial frame, deadline, or parent cancellation
// cancels promptly.
func ServeFramedStream(ctx context.Context, stream FramedStream, d *dispatch.Dispatcher, requests chan struct{}, encryptedOnly bool) error {
	if stream == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid framed stream, dispatcher, or request capacity")
	}
	return serveFramedStreamTransport(ctx, stream, d, requests, encryptedOnly, false, true, []string{"long_poll", "framed_stream"}, 3*time.Second)
}

func serveFramedStreamTransport(parent context.Context, stream FramedStream, d *dispatch.Dispatcher, requests chan struct{}, encryptedOnly, handshake, gracefulEOF bool, watchModes []string, writeTimeout time.Duration) error {
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = stream.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		_ = stream.Close()
	}()
	if handshake {
		if secure, ok := stream.(*tls.Conn); ok {
			hsctx, finish := context.WithTimeout(ctx, 3*time.Second)
			err := secure.HandshakeContext(hsctx)
			finish()
			if err != nil {
				return err
			}
			if secure.ConnectionState().NegotiatedProtocol != FramedALPN {
				return errors.New("framed TLS ALPN mismatch")
			}
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
		if err = stream.SetWriteDeadline(time.Now().Add(writeTimeout)); err == nil {
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(len(body)))
			_, err = io.Copy(stream, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(body)))
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
		if err := stream.SetReadDeadline(time.Now().Add(idle)); err != nil {
			return nil, err
		}
		var header [4]byte
		n, err := io.ReadFull(stream, header[:1])
		if err != nil {
			if n == 0 && errors.Is(err, io.EOF) && gracefulEOF {
				return nil, errFramedCleanEOF
			}
			return nil, err
		}
		first = false
		if err := stream.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(stream, header[1:]); err != nil {
			return nil, err
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > MaxRequestFrameBytes {
			return nil, store.ErrInvalid
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(stream, body); err != nil {
			return nil, err
		}
		return body, nil
	}
	return runCorrelated(ctx, correlatedTransport{next: next, write: write, watchModes: watchModes, gracefulEOF: gracefulEOF}, d, requests, encryptedOnly)
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
