package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/zmqwire"
)

// ZeroMQResponse is one whole REP reply. Only authenticated intent supplies ID.
// Local replies are clear; every request must be an encrypted signed envelope.
type ZeroMQResponse struct {
	Version   int                      `json:"version"`
	RequestID string                   `json:"request_id,omitempty"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

// ServeZeroMQ owns and joins serial REQ/REP connections. The service wraps the
// listener with shared session admission; this owner shares request admission.
func ServeZeroMQ(ctx context.Context, l net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	if l == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid ZeroMQ owner")
	}
	address, ok := l.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return errors.New("ZeroMQ requires loopback TCP")
	}
	ctx, cancel := context.WithCancel(ctx)
	var work sync.WaitGroup
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = l.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		work.Wait()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		work.Go(func() { serveZeroMQConnection(ctx, conn, d, requests) })
	}
}

func serveZeroMQConnection(ctx context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}) {
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
		_ = conn.Close()
	}()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return
	}
	greeting := zmqwire.Greeting()
	if _, err := io.Copy(conn, bytes.NewReader(greeting[:])); err != nil {
		return
	}
	if err := zmqwire.ReadGreeting(conn); err != nil {
		return
	}
	if err := zmqwire.ReadReady(conn); err != nil {
		return
	}
	if _, err := io.Copy(conn, bytes.NewReader(zmqwire.Ready())); err != nil {
		return
	}
	for ctx.Err() == nil {
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return
		}
		token, err := zmqwire.ReadRequest(conn)
		if err != nil {
			return
		}
		if err = zeroMQExchange(ctx, conn, d, requests, string(token)); err != nil {
			return
		}
	}
}

func zeroMQExchange(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, token string) error {
	response := ZeroMQResponse{Version: 1}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
		now := time.Now()
		verified, err := d.AuthenticateDetached(token, true, now)
		if err == nil {
			response.RequestID = verified.RequestID()
			switch verified.Operation() {
			case core.Send, core.ContextRead, core.HistoryRead, core.Search:
				ctx, cancel := context.WithTimeout(parent, 3*time.Second)
				result, dispatchErr := d.DetachedVerified(ctx, verified, time.Now())
				cancel()
				err = dispatchErr
				if err == nil {
					response.Result = &result
				}
			default:
				response.Error = &core.Failure{Code: core.Forbidden}
			}
		}
		if err != nil {
			status, code := httpError(err)
			response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
		}
	default:
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(body) > zmqwire.MaxData {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		body, err = json.Marshal(response)
		if err != nil {
			return err
		}
	}
	wire, err := zmqwire.Reply(body)
	if err != nil {
		return err
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if err = conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err = io.Copy(conn, bytes.NewReader(wire))
	return err
}
