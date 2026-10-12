package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

const (
	localREPMaxData = 65536
	// localREPMessageDeadline bounds reading one message once its first byte
	// has arrived, and writing one reply. An idle connection between
	// messages has no deadline: like an IRC client, a REQ socket may stay
	// connected, bounded only by the listener's connection caps.
	localREPMessageDeadline = 3 * time.Second
)

// LocalREPResponse is one whole REP reply: the request's ID and either its
// DetachedResult or a failure, as plain contract JSON.
type LocalREPResponse struct {
	Version   int                      `json:"version"`
	RequestID string                   `json:"request_id,omitempty"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

// awaitLocalREPMessage waits without a deadline for the first byte of the next
// message, then starts that message's deadline. Closing the connection (on
// shutdown) ends the wait.
func awaitLocalREPMessage(conn net.Conn, r *bufio.Reader) error {
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	if _, err := r.Peek(1); err != nil {
		return err
	}
	return conn.SetReadDeadline(time.Now().Add(localREPMessageDeadline))
}

func writeLocalREP(parent context.Context, conn net.Conn, wire []byte) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if err := conn.SetWriteDeadline(time.Now().Add(localREPMessageDeadline)); err != nil {
		return err
	}
	_, err := io.Copy(conn, bytes.NewReader(wire))
	return err
}

// localREPExchange answers one request: a plain contract request run as its
// author (or the connection's identified author).
func localREPExchange(parent context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, body []byte, encode func([]byte) ([]byte, error), plain *dispatch.Ingress) error {
	response := LocalREPResponse{Version: 1}
	select {
	case requests <- struct{}{}:
		defer func() { <-requests }()
		ctx, cancel := context.WithTimeout(parent, localREPMessageDeadline)
		localREPPlain(ctx, d, plain, body, &response)
		cancel()
	default:
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
	}
	reply, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if len(reply) > localREPMaxData {
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		if reply, err = json.Marshal(response); err != nil {
			return err
		}
	}
	wire, err := encode(reply)
	if err != nil {
		return err
	}
	return writeLocalREP(parent, conn, wire)
}

func localREPPlain(ctx context.Context, d *dispatch.Dispatcher, plain *dispatch.Ingress, body []byte, response *LocalREPResponse) {
	if plain == nil {
		response.Error = &core.Failure{Code: core.UnsupportedOperation}
		return
	}
	sub, err := core.DecodeSubmission(body)
	if err != nil {
		response.Error = &core.Failure{Code: core.InvalidRequest}
		return
	}
	response.RequestID = sub.Request.RequestID
	result, err := d.SubmitDecoded(ctx, *plain, sub, time.Now())
	if err != nil {
		_, failure := errorFailure(err)
		response.Error = &failure
		return
	}
	response.Result = &result
}

// localREPWire is one serial REQ/REP wire format over TCP.
type localREPWire struct {
	name string
	// handshake exchanges greetings within the connection's first deadline.
	handshake func(conn net.Conn, r *bufio.Reader) error
	// next reads the next message and returns its request body and reply
	// encoder. A nil encoder is traffic the wire answered itself.
	next func(ctx context.Context, conn net.Conn, r *bufio.Reader) (body []byte, encode func([]byte) ([]byte, error), err error)
}

// serveLocalREP owns and joins serial REQ/REP connections on l.
func serveLocalREP(ctx context.Context, l net.Listener, d *dispatch.Dispatcher, requests chan struct{}, wire localREPWire) error {
	if l == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid " + wire.name + " owner")
	}
	if _, ok := l.Addr().(*net.TCPAddr); !ok {
		return errors.New(wire.name + " requires TCP")
	}
	return ServeAccepted(ctx, l, func(ctx context.Context, conn net.Conn) (func(), error) {
		return func() { serveLocalREPConnection(ctx, conn, d, requests, wire) }, nil
	})
}

func serveLocalREPConnection(ctx context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}, wire localREPWire) {
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
		_ = conn.Close()
	}()
	if err := conn.SetDeadline(time.Now().Add(localREPMessageDeadline)); err != nil {
		return
	}
	r := bufio.NewReaderSize(conn, 4096)
	if err := wire.handshake(conn, r); err != nil {
		return
	}
	// One author session per connection: account.identify holds until close.
	plain := authorIngress(d, conn.RemoteAddr(), true)
	defer closeIngress(plain)
	for ctx.Err() == nil {
		if err := awaitLocalREPMessage(conn, r); err != nil {
			return
		}
		body, encode, err := wire.next(ctx, conn, r)
		if err != nil {
			return
		}
		if encode == nil {
			continue
		}
		if err = localREPExchange(ctx, conn, d, requests, body, encode, plain); err != nil {
			return
		}
	}
}
