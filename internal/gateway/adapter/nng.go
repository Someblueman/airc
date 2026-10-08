package adapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/nngwire"
)

// ServeNNG owns and joins serial REQ/REP connections. The service wraps the
// listener with shared session admission; this owner shares request admission.
func ServeNNG(ctx context.Context, l net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	if l == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid NNG owner")
	}
	address, ok := l.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		return errors.New("NNG requires loopback TCP")
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
		work.Go(func() { serveNNGConnection(ctx, conn, d, requests) })
	}
}

func serveNNGConnection(ctx context.Context, conn net.Conn, d *dispatch.Dispatcher, requests chan struct{}) {
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
	greeting := nngwire.Greeting()
	if _, err := io.Copy(conn, bytes.NewReader(greeting[:])); err != nil {
		return
	}
	if err := nngwire.ReadGreeting(conn); err != nil {
		return
	}
	for ctx.Err() == nil {
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return
		}
		token, err := nngwire.ReadRequest(conn)
		if err != nil {
			return
		}
		if err = localREPExchange(ctx, conn, d, requests, string(token.Body), func(body []byte) ([]byte, error) { return nngwire.Reply(token.Correlation, body) }); err != nil {
			return
		}
	}
}
