package adapter

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/zmqwire"
)

// ServeZeroMQ owns and joins serial REQ/REP connections (ZMTP 3.1, NULL
// mechanism). The service caps connections; this owner shares request
// admission. Like IRC, it needs no TLS: a request names its author.
func ServeZeroMQ(ctx context.Context, l net.Listener, d *dispatch.Dispatcher, requests chan struct{}) error {
	return serveLocalREP(ctx, l, d, requests, zeroMQREP)
}

var zeroMQREP = localREPWire{
	name: "ZeroMQ",
	handshake: func(conn net.Conn, r *bufio.Reader) error {
		greeting := zmqwire.Greeting()
		if _, err := io.Copy(conn, bytes.NewReader(greeting[:])); err != nil {
			return err
		}
		if err := zmqwire.ReadGreeting(r); err != nil {
			return err
		}
		if err := zmqwire.ReadReady(r); err != nil {
			return err
		}
		_, err := io.Copy(conn, bytes.NewReader(zmqwire.Ready()))
		return err
	},
	next: func(ctx context.Context, conn net.Conn, r *bufio.Reader) ([]byte, func([]byte) ([]byte, error), error) {
		traffic, err := zmqwire.ReadTraffic(r)
		if err != nil {
			return nil, nil, err
		}
		if traffic.Ping {
			// ZMTP 3.1 heartbeat: answer PING with PONG and its context.
			pong, err := zmqwire.Pong(traffic.Context)
			if err == nil {
				err = writeLocalREP(ctx, conn, pong)
			}
			return nil, nil, err
		}
		return traffic.Body, zmqwire.Reply, nil
	},
}
