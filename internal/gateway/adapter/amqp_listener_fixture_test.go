//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func amqpListenerFixture(t *testing.T, slots chan struct{}, perPrincipal int) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	f := newAMQPAdmissionFixture(t)
	tokenBytes, err := os.ReadFile(f.c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	return amqpListenerWithAdmission(t, slots, perPrincipal, f, sealer, token)
}

func amqpListenerWithAdmission(t *testing.T, slots chan struct{}, perPrincipal int, f amqpAdmissionFixture, sealer *auth.ResponseSealer, token string) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	v := f.v
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
		done <- ServeAMQP(ctx, l, v, slots, perPrincipal, config.AMQP{Routes: []config.AMQPRoute{f.route()}}, f.d, sealer, f.c.Realm, make(chan struct{}, 2))
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
	return l, dial, token, cancel, done
}
