package service

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// The listener owns accepted carriers, including handshakes, until every
// connection has joined. Admission is supplied by the shared limitedListener.
func serveCoAPTCPListener(parent context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	ctx, cancel := context.WithCancel(parent)
	var work sync.WaitGroup
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		work.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		work.Go(func() {
			defer conn.Close()
			secure, ok := conn.(*tls.Conn)
			if !ok {
				return
			}
			hsctx, finish := context.WithTimeout(ctx, 5*time.Second)
			err := secure.HandshakeContext(hsctx)
			finish()
			if err != nil || secure.ConnectionState().NegotiatedProtocol != adapter.CoAPALPN {
				return
			}
			_ = adapter.ServeCoAPTCP(ctx, secure, d, requests)
		})
	}
}
