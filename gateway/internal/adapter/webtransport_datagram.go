package adapter

import (
	"context"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/quic-go/quic-go"
	webtransport "github.com/quic-go/webtransport-go"
)

// serveWebTransportDatagrams preserves the WebTransport carrier while sharing
// the generic fragment framing and correlated operation engine.
func serveWebTransportDatagrams(parent context.Context, session *webtransport.Session, conn *quic.Conn, d *dispatch.Dispatcher, requests chan struct{}, plain *dispatch.Ingress) error {
	if session == nil || conn == nil {
		return store.ErrInvalid
	}
	return serveFragmentDatagrams(parent, session, func() { _ = conn.CloseWithError(0, "webtransport datagram write bound") }, d, requests, "webtransport_datagrams", plain)
}
