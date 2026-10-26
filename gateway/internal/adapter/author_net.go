package adapter

import (
	"net"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

// connectionIngress is implemented by streams multiplexed inside one
// connection (QUIC and WebTransport streams). Every stream of the connection
// shares its source and session, so an account.identify on one stream holds
// for the connection's other streams and datagrams until it closes.
type connectionIngress interface {
	ingress() *dispatch.Ingress
}

// authorIngress is the plain-request ingress of one connection or exchange
// from addr: the client IP address without its port (or listener:<id> for
// carriers without addresses) and, for a connection, a fresh session the
// caller must close. It is nil when the listener admits no authors.
func authorIngress(d *dispatch.Dispatcher, addr net.Addr, connection bool) *dispatch.Ingress {
	if !d.AuthorAccess() {
		return nil
	}
	in := &dispatch.Ingress{Source: d.Source(addr)}
	if connection {
		in.Session = dispatch.NewSession()
	}
	return in
}

// closeIngress forgets a connection's identified author.
func closeIngress(in *dispatch.Ingress) {
	if in != nil {
		in.Session.Close()
	}
}
