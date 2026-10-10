package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// AnonymousUDPResponse is plaintext. RequestID correlates a response but does
// not authenticate it; all callers share the listener's configured identity.
type AnonymousUDPResponse struct {
	Version   int                      `json:"version"`
	RequestID string                   `json:"request_id"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

type AnonymousUDP struct {
	dispatcher *dispatch.Dispatcher
	listener   string
	requests   chan struct{}
}

func NewAnonymousUDP(d *dispatch.Dispatcher, listener string, requests chan struct{}) (*AnonymousUDP, error) {
	if d == nil || listener == "" || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	return &AnonymousUDP{dispatcher: d, listener: listener, requests: requests}, nil
}

// Serve accepts one JSON request per datagram and sends one JSON response to
// that source socket. Requests and responses are at most 16 KiB; responses may
// not exceed their request's wire length, so this is not a UDP amplifier. A
// client can add JSON whitespace to reserve room for its response. Processing
// is serial and shares the service's request cap; no source registry is needed.
func (u *AnonymousUDP) Serve(ctx context.Context, conn net.PacketConn) error {
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		_ = conn.Close()
		if !stop() {
			<-closed
		}
	}()
	buf := make([]byte, core.MaxEnvelopeBytes+1)
	for {
		n, source, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if n == 0 || n > core.MaxEnvelopeBytes {
			continue
		}
		request, err := core.Decode(buf[:n])
		if err != nil {
			continue
		}
		select {
		case u.requests <- struct{}{}:
		default:
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		result, err := u.dispatcher.Anonymous(requestCtx, u.listener, request, time.Now())
		cancel()
		<-u.requests
		response := AnonymousUDPResponse{Version: 1, RequestID: request.RequestID, Result: &result}
		if err != nil {
			response.Result = nil
			_, code := httpError(err)
			response.Error = &core.Failure{Code: code, Transient: errors.Is(err, store.ErrCapacity) || errors.Is(err, backend.ErrReadUnavailable)}
		}
		data, marshalErr := json.Marshal(response)
		if marshalErr != nil || len(data) > n {
			response.Result = nil
			response.Error = &core.Failure{Code: core.CapacityExceeded}
			data, _ = json.Marshal(response)
		}
		if len(data) > n || ctx.Err() != nil {
			continue
		}
		if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		// Datagram delivery can fail independently for one source. It does not
		// undo admission or stop the listener; a retry keeps the request ID.
		_, _ = conn.WriteTo(data, source)
	}
}
