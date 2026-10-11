package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/pion/dtls/v3"
)

const DTLSALPN = "airc-gateway-dtls/1"

// ServeDTLSDatagrams consumes an already-handshaken DTLS connection. The
// service owns the connection's outer lifecycle; this function only bounds
// application records and joins the shared correlated dispatcher.
func ServeDTLSDatagrams(ctx context.Context, conn *dtls.Conn, d *dispatch.Dispatcher, requests chan struct{}, activity func()) error {
	if conn == nil || d == nil || cap(requests) < 1 || activity == nil {
		return store.ErrInvalid
	}
	carrier := &dtlsDatagramCarrier{conn: conn}
	// One author session per DTLS association.
	plain := authorIngress(d, conn.RemoteAddr(), true)
	defer closeIngress(plain)
	return serveAGD1Datagrams(ctx, carrier, func() { _ = conn.Close() }, d, requests, "dtls_datagrams", activity, plain)
}

type dtlsDatagramCarrier struct {
	conn *dtls.Conn
}

func (c *dtlsDatagramCarrier) SendDatagram(payload []byte) error {
	if len(payload) > agd1DatagramBytes {
		return store.ErrCapacity
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	_, err := c.conn.Write(payload)
	return err
}

func (c *dtlsDatagramCarrier) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = c.conn.SetReadDeadline(time.Now())
		close(done)
	})
	buf := make([]byte, agd1DatagramBytes+1)
	n, err := c.conn.Read(buf)
	if !stop() {
		<-done
	}
	// Pion wraps context.DeadlineExceeded in its timeout error. Preserve the
	// shared datagram loop's quiet-read semantics, and make parent cancellation
	// win over the transport's timeout result.
	if cause := ctx.Err(); cause != nil {
		return nil, cause
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		// A non-timeout read error includes Pion's unexported buffer-too-small
		// error. Close here so an oversized application record cannot leave a
		// poisoned DTLS connection available for another request.
		_ = c.conn.Close()
		return nil, err
	}
	if n > agd1DatagramBytes {
		_ = c.conn.Close()
		return nil, errors.New("DTLS application record exceeds datagram limit")
	}
	return append([]byte(nil), buf[:n]...), nil
}
