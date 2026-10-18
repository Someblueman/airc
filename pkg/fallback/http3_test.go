package fallback

import (
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testtls"
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
	deadline := time.Now().Add(2 * time.Second)
	for first.Context().Err() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
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
	deadline := time.Now().Add(2 * time.Second)
	for h.q.conn.Context().Err() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
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
