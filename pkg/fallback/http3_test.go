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
	_ = k.Close()
	if _, err := h.call(t.Context(), request{RequestID: "c", Operation: "history.read", Target: targetOf("#room"), Limit: 1}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("after Close: %v", err)
	}
}
