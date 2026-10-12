package service

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// The listener owns accepted carriers, including handshakes, until every
// connection has joined. Admission is supplied by the shared limitedListener.
func serveCoAPTCPListener(parent context.Context, listener net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	return adapter.ServeAccepted(parent, listener, func(ctx context.Context, conn net.Conn) (func(), error) {
		return func() {
			defer conn.Close()
			secure, ok := conn.(*tls.Conn)
			if !ok {
				// Cleartext CoAP over TCP: author frames, as on IRC.
				_ = adapter.ServeCoAPTCP(ctx, conn, d, requests)
				return
			}
			hsctx, finish := context.WithTimeout(ctx, 5*time.Second)
			err := secure.HandshakeContext(hsctx)
			finish()
			if err != nil || secure.ConnectionState().NegotiatedProtocol != adapter.CoAPALPN {
				return
			}
			_ = adapter.ServeCoAPTCP(ctx, secure, d, requests)
		}, nil
	})
}
