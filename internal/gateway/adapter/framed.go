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
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
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
	var work sync.WaitGroup
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		_ = conn.Close()
		work.Wait()
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
	fail := func(id string, err error) error {
		status, code := httpError(err)
		return write(FrameResponse{Version: 1, CorrelationID: id, Event: "error", Error: &core.Failure{Code: code, Transient: status == 429 || status == 503}})
	}
	active := make(map[string]bool)
	var activeMu sync.Mutex
	first := true
	for {
		idle := 30 * time.Second
		if first {
			idle = 5 * time.Second
		}
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:1]); err != nil {
			return
		}
		first = false
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(conn, header[1:]); err != nil {
			return
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > MaxRequestFrameBytes {
			return
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		var request FrameRequest
		if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) {
			return
		}
		// Check before profile validation: even a rejected wrapper must not
		// emit an error under another active request's correlation handle.
		activeMu.Lock()
		duplicate := active[request.CorrelationID]
		activeMu.Unlock()
		if duplicate {
			return
		}
		if request.Envelope == "" || (request.Profile != "signed" && request.Profile != "encrypted") {
			if fail(request.CorrelationID, store.ErrInvalid) != nil {
				return
			}
			continue
		}
		if encryptedOnly && request.Profile != "encrypted" {
			if fail(request.CorrelationID, store.ErrInvalid) != nil {
				return
			}
			continue
		}
		activeMu.Lock()
		full := len(active) >= 4
		if !full {
			active[request.CorrelationID] = true
		}
		activeMu.Unlock()
		if full {
			if fail(request.CorrelationID, store.ErrCapacity) != nil {
				return
			}
			continue
		}
		select {
		case requests <- struct{}{}:
		default:
			activeMu.Lock()
			delete(active, request.CorrelationID)
			activeMu.Unlock()
			if fail(request.CorrelationID, store.ErrCapacity) != nil {
				return
			}
			continue
		}
		work.Go(func() {
			defer func() { <-requests; activeMu.Lock(); delete(active, request.CorrelationID); activeMu.Unlock() }()
			timeout := 4 * time.Second
			if request.Stream {
				timeout = 20 * time.Second
			}
			callCtx, finish := context.WithTimeout(ctx, timeout)
			defer finish()
			encrypted := request.Profile == "encrypted"
			if !request.Stream {
				result, err := d.Detached(callCtx, request.Envelope, encrypted, time.Now())
				if err != nil {
					_ = fail(request.CorrelationID, err)
					return
				}
				if result.Capabilities != nil {
					result.Capabilities.MaxRequestFrameBytes = MaxRequestFrameBytes
					result.Capabilities.MaxResponseFrameBytes = MaxResponseFrameBytes
					if len(result.Capabilities.WatchModes) > 0 {
						result.Capabilities.WatchModes = []string{"long_poll", "framed_stream"}
					}
				}
				if err = write(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "result", Result: &result}); errors.Is(err, store.ErrCapacity) {
					_ = fail(request.CorrelationID, err)
				}
				return
			}
			started, bytes, frames := false, 0, 0
			err := d.DetachedWatch(callCtx, request.Envelope, encrypted, time.Now(), func(page backend.HistoryPage) error {
				response := FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "page", Page: &page}
				body, e := json.Marshal(response)
				if e != nil {
					return store.ErrInvalid
				}
				if len(body) > MaxResponseFrameBytes || bytes+len(body)+4 > MaxStreamBytes-1024 || frames >= MaxStreamFrames {
					return store.ErrCapacity
				}
				if e = write(response); e != nil {
					return e
				}
				started = true
				bytes += len(body) + 4
				frames++
				return nil
			})
			if ctx.Err() != nil {
				return
			}
			if !started {
				if err != nil {
					_ = fail(request.CorrelationID, err)
				}
				return
			}
			reason := "resume"
			if errors.Is(err, backend.ErrStreamRenew) {
				reason = "lease_expired"
			} else if errors.Is(err, store.ErrCapacity) {
				reason = "capacity"
			} else if err != nil {
				reason = "upstream_unavailable"
			}
			_ = write(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "end", Reason: reason})
		})
	}
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
