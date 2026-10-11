package adapter

import (
	"bufio"
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

// ServeNNG owns and joins serial REQ0/REP0 connections. The service caps
// connections; this owner shares request admission. Like IRC, it needs no
// TLS: a request names its author.
func ServeNNG(ctx context.Context, l net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	if l == nil || d == nil || cap(requests) < 1 {
		return errors.New("invalid NNG owner")
	}
	if _, ok := l.Addr().(*net.TCPAddr); !ok {
		return errors.New("NNG requires TCP")
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
	if err := conn.SetDeadline(time.Now().Add(localREPMessageDeadline)); err != nil {
		return
	}
	greeting := nngwire.Greeting()
	if _, err := io.Copy(conn, bytes.NewReader(greeting[:])); err != nil {
		return
	}
	r := bufio.NewReaderSize(conn, 4096)
	if err := nngwire.ReadGreeting(r); err != nil {
		return
	}
	// One author session per connection: account.identify holds until close.
	plain := authorIngress(d, conn.RemoteAddr(), true)
	defer closeIngress(plain)
	for ctx.Err() == nil {
		if err := awaitLocalREPMessage(conn, r); err != nil {
			return
		}
		request, err := nngwire.ReadRequest(r)
		if err != nil {
			return
		}
		if err = localREPExchange(ctx, conn, d, requests, request.Body, func(body []byte) ([]byte, error) { return nngwire.Reply(request.Correlation, body) }, plain); err != nil {
			return
		}
	}
}
