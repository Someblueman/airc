package fallback

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/quic-go/quic-go/http3"
)

// serveHTTP3 also serves g's routes over HTTP/3 on a loopback UDP port.
func serveHTTP3(t *testing.T, g *fakeGateway) (int, *tls.Config) {
	t.Helper()
	cert := testtls.New(t)
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	config := cert.Server.Clone()
	config.NextProtos = []string{http3.NextProtoH3}
	srv := &http3.Server{Handler: g.srv.Config.Handler, TLSConfig: config}
	go func() { _ = srv.Serve(udp) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = udp.Close()
	})
	return udp.LocalAddr().(*net.UDPAddr).Port, cert.Client
}

// An http3 endpoint is TLS however it is written, is reached by name (every
// address of the host is tried), and dials again once the gateway has closed
// its connection.
func TestHTTP3EndpointDialsByNameAndRedials(t *testing.T) {
	g := newFakeGateway(t)
	port, trust := serveHTTP3(t, g)
	e, err := ParseEndpoint("http3:localhost:" + strconv.Itoa(port))
	if err != nil || !e.TLS {
		t.Fatalf("%+v %v", e, err)
	}
	if (&Client{Host: "127.0.0.1"}).usable(Endpoint{Carrier: protocol.Carrier{Name: "http3", Path: "/run/gateway.sock"}}) == nil {
		t.Fatal("an http3 socket path was usable")
	}
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: trust, Endpoints: []Endpoint{e}}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h := k.gateway.(*http3Conn)
	if _, err := h.call(t.Context(), request{RequestID: "a", Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
		t.Fatal(err)
	}
	first := h.q.conn
	_ = first.CloseWithError(0, "idle") // as the gateway does after thirty idle seconds
	select {
	case <-first.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the closed connection did not end")
	}
	if _, err := h.call(t.Context(), request{RequestID: "b", Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
		t.Fatalf("after the connection closed: %v", err)
	}
	if h.q.conn == first {
		t.Fatal("did not dial again")
	}
	// The redial resumed the TLS session and sent its request in the
	// first flight.
	if !h.Resumed0RTT() {
		t.Fatal("the redial did not use 0-RTT")
	}
	_ = k.Close()
	if _, err := h.call(t.Context(), request{RequestID: "c", Operation: "history.read", Target: targetOf("#room"), Limit: 1}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("after Close: %v", err)
	}
}

// A gateway that restarts loses its ticket keys and refuses a resumed
// session's 0-RTT: the next request still succeeds, on the same
// connection once its handshake completes, rather than failing until the
// connection idles out.
func TestHTTP3RecoversFromRejected0RTT(t *testing.T) {
	g := newFakeGateway(t)
	cert := testtls.New(t)
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	serve := func(udp *net.UDPConn, key byte) *http3.Server {
		config := cert.Server.Clone()
		config.NextProtos = []string{http3.NextProtoH3}
		config.SetSessionTicketKeys([][32]byte{{key}})
		srv := &http3.Server{Handler: g.srv.Config.Handler, TLSConfig: config}
		go func() { _ = srv.Serve(udp) }()
		return srv
	}
	first := serve(udp, 1)
	e, _ := ParseEndpoint("http3:localhost:" + strconv.Itoa(port))
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: cert.Client, Endpoints: []Endpoint{e}}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	h := k.gateway.(*http3Conn)
	read := func(id string) error {
		_, err := h.call(t.Context(), request{RequestID: id, Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"})
		return err
	}
	if err := read("a"); err != nil {
		t.Fatal(err)
	}
	// The gateway restarts with new ticket keys; the client redials with
	// its cached session.
	_ = first.Close()
	_ = udp.Close()
	select {
	case <-h.q.conn.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the connection did not notice the gateway going away")
	}
	udp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	second := serve(udp, 2)
	t.Cleanup(func() { _ = second.Close(); _ = udp.Close() })
	if err := read("b"); err != nil {
		t.Fatalf("after the gateway restarted: %v", err)
	}
	if err := read("c"); err != nil {
		t.Fatalf("the request after: %v", err)
	}
}

// Dials that resume the same cached session at once (a hedged follow's two
// legs, or a host's addresses raced) each resume their own copy of it:
// quic-go writes to the session it resumes.
func TestHTTP3ConcurrentDialsResumeTheSameSession(t *testing.T) {
	g := newFakeGateway(t)
	port, trust := serveHTTP3(t, g)
	e, _ := ParseEndpoint("http3:localhost:" + strconv.Itoa(port))
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: trust, Endpoints: []Endpoint{e}}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.gateway.call(t.Context(), request{RequestID: "a", Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
		t.Fatal(err)
	}
	testwait.Every(t, 2*time.Second, 10*time.Millisecond, "the session ticket to be cached", func() bool {
		_, ok := quicSessions.Get("localhost")
		return ok
	})
	_ = k.Close()
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			k, err := c.Connect(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			defer k.Close()
			if _, err := k.gateway.call(t.Context(), request{RequestID: strconv.Itoa(i), Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
				t.Error(err)
			}
			if !k.gateway.(*http3Conn).Resumed0RTT() {
				t.Error("a concurrent dial did not resume the session")
			}
		})
	}
	wg.Wait()
}

// A host's addresses are raced, and one that never answers loses, even when a
// resumed session lets its dial return before the handshake (0-RTT): the
// request is not handed to it, and a call does not wait for ever on a
// connection whose handshake never completes. On a host that resolves
// localhost to ::1 as well as 127.0.0.1, with the gateway on one of them,
// this is every redial.
func TestHTTP3DialSkipsAnAddressThatDoesNotAnswer(t *testing.T) {
	g := newFakeGateway(t)
	port, trust := serveHTTP3(t, g)
	previous := lookupNetIP
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	t.Cleanup(func() { lookupNetIP = previous })
	e, err := ParseEndpoint("http3:localhost:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: trust, Endpoints: []Endpoint{e}}
	k, err := c.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	h := k.gateway.(*http3Conn)
	read := func(id string) error {
		_, err := h.call(ctx, request{RequestID: id, Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"})
		return err
	}
	if err := read("a"); err != nil {
		t.Fatal(err)
	}
	first := h.q.conn
	_ = first.CloseWithError(0, "idle")
	select {
	case <-first.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the closed connection did not end")
	}
	if err := read("b"); err != nil {
		t.Fatalf("after the connection closed: %v", err)
	}
	if h.q.conn == first {
		t.Fatal("did not dial again")
	}
	if !h.Resumed0RTT() {
		t.Fatal("the redial did not resume the session")
	}
}

// The same recovery when the host has several addresses: the race then
// waits for the winner's handshake, during which the restarted gateway
// rejects the resumed session's 0-RTT, and the HTTP/3 client must be built
// on the connection quic-go hands over after the rejection.
func TestHTTP3RecoversFromRejected0RTTAcrossAddresses(t *testing.T) {
	previous := lookupNetIP
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	t.Cleanup(func() { lookupNetIP = previous })
	g := newFakeGateway(t)
	cert := testtls.New(t)
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	serve := func(udp *net.UDPConn, key byte) *http3.Server {
		config := cert.Server.Clone()
		config.NextProtos = []string{http3.NextProtoH3}
		config.SetSessionTicketKeys([][32]byte{{key}})
		srv := &http3.Server{Handler: g.srv.Config.Handler, TLSConfig: config}
		go func() { _ = srv.Serve(udp) }()
		return srv
	}
	first := serve(udp, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	e, _ := ParseEndpoint("http3:localhost:" + strconv.Itoa(port))
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: cert.Client, Endpoints: []Endpoint{e}}
	k, err := c.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	h := k.gateway.(*http3Conn)
	read := func(id string) error {
		_, err := h.call(ctx, request{RequestID: id, Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"})
		return err
	}
	if err := read("a"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	_ = udp.Close()
	select {
	case <-h.q.conn.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the connection did not notice the gateway going away")
	}
	udp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	second := serve(udp, 2)
	t.Cleanup(func() { _ = second.Close(); _ = udp.Close() })
	if err := read("b"); err != nil {
		t.Fatalf("after the gateway restarted: %v", err)
	}
	if err := read("c"); err != nil {
		t.Fatalf("the request after: %v", err)
	}
}

// A failed dial does not forget the session the others resume: crypto/tls
// evicts a session when a handshake resuming it fails, and a losing address
// in a race fails that way.
func TestSessionCacheKeepsASessionAFailedDialWouldEvict(t *testing.T) {
	cache := copiedSessions{tls.NewLRUClientSessionCache(4)}
	g := newFakeGateway(t)
	port, trust := serveHTTP3(t, g)
	e, _ := ParseEndpoint("http3:127.0.0.1:" + strconv.Itoa(port))
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: trust, Endpoints: []Endpoint{e}}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	var cs *tls.ClientSessionState
	testwait.Every(t, 2*time.Second, 10*time.Millisecond, "a session to be cached", func() bool {
		var ok bool
		cs, ok = quicSessions.Get("127.0.0.1")
		return ok
	})
	cache.Put("gateway", cs)
	cache.Put("gateway", nil) // what a failed resumption does
	if _, ok := cache.Get("gateway"); !ok {
		t.Fatal("the eviction was not ignored")
	}
}

// The same concurrent resumption when the host has several addresses and
// the first never answers, which fails that address's dial while the others
// resume: every dial still resumes the session.
func TestHTTP3ConcurrentDialsResumeAcrossAddresses(t *testing.T) {
	previous := lookupNetIP
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	t.Cleanup(func() { lookupNetIP = previous })
	g := newFakeGateway(t)
	port, trust := serveHTTP3(t, g)
	e, _ := ParseEndpoint("http3:localhost:" + strconv.Itoa(port))
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: trust, Endpoints: []Endpoint{e}}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.gateway.call(t.Context(), request{RequestID: "a", Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
		t.Fatal(err)
	}
	testwait.Every(t, 2*time.Second, 10*time.Millisecond, "the session ticket to be cached", func() bool {
		_, ok := quicSessions.Get("localhost")
		return ok
	})
	_ = k.Close()
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			k, err := c.Connect(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			defer k.Close()
			if _, err := k.gateway.call(t.Context(), request{RequestID: strconv.Itoa(i), Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
				t.Error(err)
			}
			if !k.gateway.(*http3Conn).Resumed0RTT() {
				t.Error("a concurrent dial did not resume the session")
			}
		})
	}
	wg.Wait()
}
