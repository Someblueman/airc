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
	"github.com/Someblueman/airc/internal/gateway/zmqwire"
)

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
		if err = localREPExchange(ctx, conn, d, requests, string(token), zmqwire.Reply); err != nil {
			return
		}
	}
}
