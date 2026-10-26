package adapter

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
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
	Profile       string `json:"profile,omitempty"`
	Stream        bool   `json:"stream,omitempty"`
}

type FrameResponse struct {
	Version       int                      `json:"version"`
	CorrelationID string                   `json:"correlation_id"`
	Event         string                   `json:"event"`
	Sequence      uint64                   `json:"sequence,omitempty"`
	Result        *dispatch.DetachedResult `json:"result,omitempty"`
	Page          *backend.HistoryPage     `json:"page,omitempty"`
	Check         *core.CheckResult        `json:"check,omitempty"`
	Reason        string                   `json:"reason,omitempty"`
	Error         *core.Failure            `json:"error,omitempty"`

	// encoded is this response's JSON when it was already encoded with
	// Sequence encodedSequence, so a transport need not encode it again.
	encoded         []byte
	encodedSequence uint64
}

// encodeFrame returns the response's JSON, reusing an encoding made for the
// same sequence number.
func encodeFrame(response FrameResponse) ([]byte, error) {
	if response.encoded != nil && response.encodedSequence == response.Sequence {
		return response.encoded, nil
	}
	return json.Marshal(response)
}

// ServeFramed owns and joins every accepted connection and request. The caller
// bounds accepted connections; requests share a separate global admission cap.
// TLS is optional, as on IRC; without it account tokens travel in clear.
func ServeFramed(ctx context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	if d == nil || cap(requests) < 1 {
		return errors.New("invalid framed dispatcher or request capacity")
	}
	return ServeAccepted(ctx, listener, func(ctx context.Context, conn net.Conn) (func(), error) {
		return func() { serveFramedConnection(ctx, conn, d, requests) }, nil
	})
}

func serveFramedConnection(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}) {
	_ = serveFramedStreamTransport(parent, conn, d, requests, true, false, []string{"long_poll", "framed_stream"}, 3*time.Second)
}

// ServeFramedStream serves one already-connected framed stream and owns its
// lifetime. A clean EOF between frames drains admitted requests before the
// stream is cancelled; a partial frame, deadline, or parent cancellation
// cancels promptly.
func ServeFramedStream(ctx context.Context, stream FramedStream, d *dispatch.Dispatcher, requests chan struct{}) error {
	if stream == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid framed stream, dispatcher, or request capacity")
	}
	return serveFramedStreamTransport(ctx, stream, d, requests, false, true, []string{"long_poll", "framed_stream"}, 3*time.Second)
}

func serveFramedStreamTransport(parent context.Context, stream FramedStream, d *dispatch.Dispatcher, requests chan struct{}, handshake, gracefulEOF bool, watchModes []string, writeTimeout time.Duration) error {
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
		body, err := encodeFrame(response)
		if err != nil || len(body) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		writes.Lock()
		defer writes.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = stream.SetWriteDeadline(time.Now().Add(writeTimeout)); err == nil {
			// One write: a 4-byte length, then the body.
			frame := make([]byte, 4, 4+len(body))
			binary.BigEndian.PutUint32(frame, uint32(len(body)))
			_, err = stream.Write(append(frame, body...))
		}
		if err != nil {
			cancel()
		}
		return err
	}
	// waiting is true while the read for a frame's first byte runs; a
	// change in whether a request is active moves its deadline.
	var idleMu sync.Mutex
	first, active, waiting := true, false, false
	idle := func() time.Duration {
		switch {
		case first:
			return 5 * time.Second
		case active:
			return streamIdle
		}
		return ConnectionIdle
	}
	streaming := func(streaming bool) {
		idleMu.Lock()
		defer idleMu.Unlock()
		if active != streaming {
			active = streaming
			if waiting {
				_ = stream.SetReadDeadline(time.Now().Add(idle()))
			}
		}
	}
	next := func() ([]byte, error) {
		idleMu.Lock()
		waiting = true
		err := stream.SetReadDeadline(time.Now().Add(idle()))
		idleMu.Unlock()
		if err != nil {
			return nil, err
		}
		var header [4]byte
		n, err := io.ReadFull(stream, header[:1])
		idleMu.Lock()
		waiting = false
		if err == nil {
			first = false
		}
		idleMu.Unlock()
		if err != nil {
			if n == 0 && errors.Is(err, io.EOF) && gracefulEOF {
				return nil, errFramedCleanEOF
			}
			return nil, err
		}
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
	var plain *dispatch.Ingress
	if shared, ok := stream.(connectionIngress); ok {
		// A stream inside a multiplexed connection (QUIC, WebTransport)
		// shares that connection's source and identified author.
		plain = shared.ingress()
	} else if d.AuthorAccess() {
		source := ""
		if conn, ok := stream.(net.Conn); ok {
			source = d.Source(conn.RemoteAddr())
		}
		plain = &dispatch.Ingress{Source: source, Session: dispatch.NewSession()}
		defer plain.Session.Close()
	}
	return runCorrelated(ctx, correlatedTransport{next: next, write: write, watchModes: watchModes, gracefulEOF: gracefulEOF, plain: plain, streaming: streaming}, d, requests)
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
