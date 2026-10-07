//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

func stompListenerFixture(t *testing.T, slots chan struct{}, perPrincipal int) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	l, dial, token, cancel, done, _, _ := stompDispatchFixture(t, slots, perPrincipal)
	return l, dial, token, cancel, done
}

func stompDispatchFixture(t *testing.T, slots chan struct{}, perPrincipal int) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error, amqpAdmissionFixture, ed25519.PublicKey) {
	t.Helper()
	return stompDispatchFixtureRequests(t, slots, perPrincipal, make(chan struct{}, 2))
}

func stompDispatchFixtureRequests(t *testing.T, slots chan struct{}, perPrincipal int, requests chan struct{}) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error, amqpAdmissionFixture, ed25519.PublicKey) {
	t.Helper()
	f := newAMQPAdmissionFixture(t)
	token, err := auth.ReadToken(f.c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := tls.NewListener(raw, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- ServeSTOMP(ctx, l, f.v, slots, perPrincipal, config.STOMP{Routes: []config.STOMPRoute{stompAdmissionRoute(f)}}, f.c.Realm, f.d, sealer, requests)
	}()
	t.Cleanup(func() { cancel(); _ = l.Close(); <-finished })
	dial := func() *tls.Conn {
		conn, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		client := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "example.com"})
		t.Cleanup(func() { closeMQTTConnection(client) })
		if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return client
	}
	return l, dial, token, cancel, done, f, pub
}

func stompClientConnect(t *testing.T, client *tls.Conn, token string) (*bufio.Reader, stompwire.Frame) {
	t.Helper()
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := stompConnect("CONNECT", token)
	f.Headers[4].Value = "0,0"
	f.Headers[1].Value = "amqp"
	wire, err := stompwire.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(client)
	f, err = stompwire.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	return r, f
}

func stompClientWrite(t *testing.T, conn net.Conn, f stompwire.Frame) {
	t.Helper()
	wire, err := stompwire.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(wire); err != nil {
		t.Fatal(err)
	}
}
