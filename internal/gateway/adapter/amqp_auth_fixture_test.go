//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

type amqpAuthResult struct {
	session *dispatch.Session
	err     error
}

// amqpAuthFixture runs the security layer over a pipe: TLS 1.3 when secure,
// and an access password when password is not empty.
func amqpAuthFixture(t *testing.T, secure bool, password string) (net.Conn, net.Conn, context.CancelFunc, <-chan amqpAuthResult) {
	return amqpAuthFixtureVersion(t, secure, password, tls.VersionTLS13)
}

func amqpAuthFixtureVersion(t *testing.T, secure bool, password string, version uint16) (net.Conn, net.Conn, context.CancelFunc, <-chan amqpAuthResult) {
	t.Helper()
	f := newRouteFixture(t)
	l := amqpFixtureListener()
	l.PasswordFile = password
	d := f.listener(t, l)
	var client, server net.Conn
	if secure {
		c, s := amqpTLSPair(t, version)
		client, server = c, s
	} else {
		server, client = net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan amqpAuthResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		session := dispatch.NewSession()
		err := openAMQPSecurity(ctx, server, d, session)
		done <- amqpAuthResult{session, err}
	}()
	t.Cleanup(func() { cancel(); closeMQTTConnection(client); closeMQTTConnection(server); <-finished })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if tc, ok := client.(*tls.Conn); ok {
		if err := tc.HandshakeContext(t.Context()); err != nil && version == tls.VersionTLS13 {
			t.Fatal(err)
		}
	}
	return client, server, cancel, done
}

func amqpFixtureListener() config.Listener {
	ops := []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}
	return config.Listener{ID: "amqp", Protocol: "amqp", Operations: ops, AMQP: &config.AMQP{Routes: []config.AMQPRoute{amqpFixtureRoute()}}}
}

func amqpFixtureRoute() config.AMQPRoute {
	return config.AMQPRoute{RequestAddress: "request", ResponseAddress: "response", Target: core.Target{Kind: "room", Name: "#fixture"}, Operations: []core.Operation{core.Send, core.ContextRead, core.HistoryRead, core.Search}}
}

// amqpClientHeader sends a protocol header, as AMQP clients speak first, and
// reads the server's answer.
func amqpClientHeader(t *testing.T, conn net.Conn, protocol byte) {
	t.Helper()
	want := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
	if _, err := conn.Write(want); err != nil {
		t.Fatal(err)
	}
	var wire [8]byte
	if _, err := io.ReadFull(conn, wire[:]); err != nil || !bytes.Equal(wire[:], want) {
		t.Fatal(wire, err)
	}
}

// amqpClientMechanisms opens SASL and checks the offered mechanisms.
func amqpClientMechanisms(t *testing.T, conn net.Conn, want ...string) {
	t.Helper()
	amqpClientHeader(t, conn, amqpwire.SASLProtocol)
	p, err := readAMQPSASL(conn)
	if err != nil || p.Kind != 0x40 || len(p.Fields) != 1 || p.Fields[0].Code != 0xe0 || len(p.Fields[0].Children) != len(want) {
		t.Fatal(p, err)
	}
	for i, mechanism := range want {
		if string(p.Fields[0].Children[i].Data) != mechanism {
			t.Fatal("mechanisms", p.Fields[0].Children)
		}
	}
}

func amqpAuthFinished(t *testing.T, done <-chan amqpAuthResult) amqpAuthResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("security layer owner did not join")
		return amqpAuthResult{}
	}
}

func amqpTLSPair(t *testing.T, version uint16) (*tls.Conn, *tls.Conn) {
	t.Helper()
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	server, peer := net.Pipe()
	secure := tls.Server(server, &tls.Config{MinVersion: version, Certificates: []tls.Certificate{pair}})
	client := tls.Client(peer, &tls.Config{MinVersion: version, MaxVersion: version, RootCAs: roots, ServerName: "example.com"})
	t.Cleanup(func() { closeMQTTConnection(client); closeMQTTConnection(secure) })
	return client, secure
}
