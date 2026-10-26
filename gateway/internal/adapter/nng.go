package adapter

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/nngwire"
)

// ServeNNG owns and joins serial REQ0/REP0 connections. The service caps
// connections; this owner shares request admission. Like IRC, it needs no
// TLS: a request names its author.
func ServeNNG(ctx context.Context, l net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	return serveLocalREP(ctx, l, d, requests, nngREP)
}

var nngREP = localREPWire{
	name: "NNG",
	handshake: func(conn net.Conn, r *bufio.Reader) error {
		greeting := nngwire.Greeting()
		if _, err := io.Copy(conn, bytes.NewReader(greeting[:])); err != nil {
			return err
		}
		return nngwire.ReadGreeting(r)
	},
	next: func(_ context.Context, _ net.Conn, r *bufio.Reader) ([]byte, func([]byte) ([]byte, error), error) {
		request, err := nngwire.ReadRequest(r)
		if err != nil {
			return nil, nil, err
		}
		return request.Body, func(body []byte) ([]byte, error) { return nngwire.Reply(request.Correlation, body) }, nil
	},
}
