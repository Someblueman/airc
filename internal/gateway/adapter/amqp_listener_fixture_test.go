//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// amqpListenerPassword is the fixture listener's access password, sent as
// the SASL PLAIN password.
const amqpListenerPassword = "letmein"

func amqpListenerFixture(t *testing.T, slots chan struct{}) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	return amqpListenerWithAdmission(t, slots, newAMQPAuthorFixture(t))
}

// amqpListenerWithAdmission serves the fixture's route over TLS on a listener
// whose access password is amqpListenerPassword.
func amqpListenerWithAdmission(t *testing.T, slots chan struct{}, f amqpAuthorFixture) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	l := amqpFixtureListener()
	l.PasswordFile = amqpListenerPassword
	d := f.listener(t, l)
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := tls.NewListener(raw, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- ServeAMQP(ctx, listener, slots, config.AMQP{Routes: []config.AMQPRoute{f.route()}}, d, make(chan struct{}, 2))
	}()
	t.Cleanup(func() { cancel(); _ = listener.Close(); <-finished })
	dial := func() *tls.Conn {
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
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
	return listener, dial, amqpListenerPassword, cancel, done
}
