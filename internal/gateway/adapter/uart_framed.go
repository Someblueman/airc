package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// ServeUART serves one serial session with the ordinary framing: under TLS
// with the framed ALPN when the listener has certificates, else in the clear
// (plain author requests; the serial line is the access boundary).
// At 115200 baud, a maximum 64 KiB response needs about six seconds on the wire;
// ten seconds bounds that write without changing other carriers' deadlines.
func ServeUART(ctx context.Context, stream FramedStream, d *dispatch.Dispatcher, requests chan struct{}) error {
	if stream == nil || d == nil || cap(requests) < 1 {
		return errors.New("UART requires a stream, a dispatcher, and request capacity")
	}
	if conn, secure := stream.(*tls.Conn); secure {
		return serveFramedStreamTransport(ctx, conn, d, requests, false, true, true, []string{"long_poll", "framed_stream"}, 10*time.Second)
	}
	if !d.AuthorAccess() {
		return errors.New("UART without TLS requires an author listener")
	}
	return serveFramedStreamTransport(ctx, stream, d, requests, false, false, true, []string{"long_poll", "framed_stream"}, 10*time.Second)
}
