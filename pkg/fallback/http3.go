package fallback

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// dialHTTP3 connects to an http3 endpoint: the HTTP routes over QUIC, always
// with TLS. QUIC runs over UDP, so it reaches a gateway where TCP is blocked
// or throttled, and its connection survives the client's address changing (a
// NAT rebinding, or Wi-Fi giving way to cellular) without a new handshake.
// HTTP proxies carry no UDP, so they do not apply.
func (c *Client) dialHTTP3(ctx context.Context, e Endpoint, progress func()) (*http3Conn, error) {
	e.TLS = true
	host := c.host(e)
	ip, err := netip.ParseAddr(host)
	if err != nil {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		ip = ips[0]
	}
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: "udp", Err: err}
	}
	transport := &quic.Transport{Conn: udp}
	keepAlive := c.KeepAlive
	if keepAlive <= 0 {
		keepAlive = 15 * time.Second
	}
	conn, err := transport.Dial(ctx, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip.Unmap(), uint16(e.Port))), c.tlsConfig(e, http3.NextProtoH3), &quic.Config{KeepAlivePeriod: keepAlive, MaxIdleTimeout: 2*keepAlive + 5*time.Second})
	if err != nil {
		_ = transport.Close()
		_ = udp.Close()
		// The handshake never completed: nothing was sent to the gateway.
		return nil, &net.OpError{Op: "dial", Net: "udp", Err: err}
	}
	if progress != nil {
		progress()
	}
	h := &http3Conn{quic: conn, transport: transport, udp: udp}
	h.client = &http.Client{Transport: (&http3.Transport{}).NewClientConn(conn)}
	h.url = "https://" + net.JoinHostPort(host, strconv.Itoa(e.Port)) + "/v1/request"
	return h, nil
}

// http3Conn is one QUIC connection carrying contract requests.
type http3Conn struct {
	httpConn
	quic      *quic.Conn
	transport *quic.Transport
	udp       *net.UDPConn
}

func (h *http3Conn) Close() error {
	_ = h.quic.CloseWithError(0, "")
	_ = h.transport.Close()
	return h.udp.Close()
}
