package adapter

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/quic-go/quic-go"
	webtransport "github.com/quic-go/webtransport-go"
)

// serveWebTransportDatagrams preserves the WebTransport carrier while sharing
// the generic AGD1 framing and correlated operation engine.
func serveWebTransportDatagrams(parent context.Context, session *webtransport.Session, conn *quic.Conn, d *dispatch.Dispatcher, requests chan struct{}) error {
	if session == nil || conn == nil {
		return store.ErrInvalid
	}
	return serveAGD1Datagrams(parent, session, func() { _ = conn.CloseWithError(0, "webtransport datagram write bound") }, d, requests, "webtransport_datagrams", nil)
}
