package fallback

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
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
	h := &http3Conn{c: c, e: e}
	q, err := h.dial(ctx, progress)
	if err != nil {
		return nil, err
	}
	h.use(q)
	h.url = "https://" + net.JoinHostPort(c.host(e), strconv.Itoa(e.Port)) + "/v1/request"
	return h, nil
}

// quicSessions is the TLS session cache every QUIC connection shares, keyed
// by server name, so a redial resumes the session and may send 0-RTT.
var quicSessions = copiedSessions{tls.NewLRUClientSessionCache(64)}

// copiedSessions hands each dial its own copy of a cached session: quic-go
// writes to the session it resumes, and dials resume one at once (a hedged
// follow's two legs, or a host's addresses raced).
type copiedSessions struct{ tls.ClientSessionCache }

func (s copiedSessions) Get(key string) (*tls.ClientSessionState, bool) {
	cs, ok := s.ClientSessionCache.Get(key)
	if !ok {
		return nil, false
	}
	ticket, state, err := cs.ResumptionState()
	if err != nil || state == nil {
		return nil, false
	}
	data, err := state.Bytes()
	if err != nil {
		return nil, false
	}
	if state, err = tls.ParseSessionState(data); err != nil {
		return nil, false
	}
	if cs, err = tls.NewResumptionState(ticket, state); err != nil {
		return nil, false
	}
	return cs, true
}

func (c *Client) sessions() tls.ClientSessionCache { return quicSessions }

// Resumed0RTT reports whether the QUIC connection in use sent 0-RTT data:
// a resumed session whose first request rode the handshake.
func (h *http3Conn) Resumed0RTT() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.q != nil && h.q.conn.ConnectionState().Used0RTT
}

// http3Conn carries contract requests over one QUIC connection at a time,
// dialling a new one when the gateway has closed the last (it closes idle
// connections after thirty seconds).
type http3Conn struct {
	httpConn
	c   *Client
	e   Endpoint
	mu  sync.Mutex
	q   *quicConn
	err error // set once closed
}

// quicConn is a QUIC connection with its own UDP socket.
type quicConn struct {
	conn      *quic.Conn
	transport *quic.Transport
}

func (q *quicConn) close() {
	_ = q.conn.CloseWithError(0, "")
	_ = q.transport.Close()
	_ = q.transport.Conn.Close()
}

func (h *http3Conn) use(q *quicConn) {
	h.q = q
	h.client = &http.Client{Transport: (&http3.Transport{}).NewClientConn(q.conn)}
}

func (h *http3Conn) call(ctx context.Context, r request) (detached, error) {
	h.mu.Lock()
	if h.err != nil {
		h.mu.Unlock()
		return detached{}, h.err
	}
	if h.q.conn.Context().Err() != nil {
		// The gateway closed the connection: nothing in flight is lost.
		h.q.close()
		q, err := h.dial(ctx, nil)
		if err != nil {
			h.mu.Unlock()
			return detached{}, err
		}
		h.use(q)
	}
	q := h.q
	h.mu.Unlock()
	d, err := h.httpConn.call(ctx, r)
	if !errors.Is(err, quic.Err0RTTRejected) {
		return d, err
	}
	// The gateway refused the resumed session's 0-RTT (it restarted, and
	// its ticket keys with it): nothing sent early reached it. The
	// connection carries on after its full handshake, under a new HTTP/3
	// client, and the request is sent again.
	h.mu.Lock()
	if h.err != nil {
		h.mu.Unlock()
		return detached{}, h.err
	}
	if h.q == q {
		next, err := q.conn.NextConnection(ctx)
		if err != nil {
			h.mu.Unlock()
			return detached{}, err
		}
		h.use(&quicConn{conn: next, transport: q.transport})
	}
	h.mu.Unlock()
	return h.httpConn.call(ctx, r)
}

func (h *http3Conn) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err == nil {
		h.err = net.ErrClosed
		h.q.close()
	}
	return nil
}

// http3Stagger is how long one address of a host gets before the next is
// tried alongside it.
const http3Stagger = 300 * time.Millisecond

// dial races a QUIC connection to each of the host's addresses, a stagger
// apart, and keeps the first to complete its handshake. A failure is a
// net.OpError: nothing was sent to the gateway.
func (h *http3Conn) dial(ctx context.Context, progress func()) (*quicConn, error) {
	host := h.c.host(h.e)
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else if addrs, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host); err != nil {
		return nil, &net.OpError{Op: "dial", Net: "udp", Err: err}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		q   *quicConn
		err error
	}
	results := make(chan result, len(addrs))
	for i, addr := range addrs {
		go func() {
			select {
			case <-time.After(time.Duration(i) * http3Stagger):
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			q, err := h.dialAddr(ctx, netip.AddrPortFrom(addr.Unmap(), uint16(h.e.Port)), progress)
			results <- result{q, err}
		}()
	}
	var errs []error
	for range addrs {
		r := <-results
		if r.err == nil {
			cancel()
			go func(left int) { // close any that also connected
				for range left {
					if late := <-results; late.q != nil {
						late.q.close()
					}
				}
			}(len(addrs) - len(errs) - 1)
			return r.q, nil
		}
		errs = append(errs, r.err)
	}
	return nil, &net.OpError{Op: "dial", Net: "udp", Err: errors.Join(errs...)}
}

func (h *http3Conn) dialAddr(ctx context.Context, addr netip.AddrPort, progress func()) (*quicConn, error) {
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	// The transport keeps the *net.UDPConn itself, for quic-go's batched
	// reads and buffer sizing; the race learns of the connection once its
	// handshake completes.
	transport := &quic.Transport{Conn: udp}
	keepAlive := h.c.KeepAlive
	if keepAlive <= 0 {
		keepAlive = 15 * time.Second
	}
	// A resumed session sends its first request in the handshake's first
	// flight (0-RTT): the gateway's contract is replay-safe, and a redial
	// after the gateway closed an idle connection costs no round trip.
	tlsConfig := h.c.tlsConfig(h.e, http3.NextProtoH3)
	tlsConfig.ClientSessionCache = h.c.sessions()
	conn, err := transport.DialEarly(ctx, net.UDPAddrFromAddrPort(addr), tlsConfig, &quic.Config{KeepAlivePeriod: keepAlive, MaxIdleTimeout: 2*keepAlive + 5*time.Second})
	if err != nil {
		_ = transport.Close()
		_ = udp.Close()
		return nil, err
	}
	if progress != nil {
		progress()
	}
	return &quicConn{conn: conn, transport: transport}, nil
}
