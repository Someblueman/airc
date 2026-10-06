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

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/config"
)

func amqpListenerFixture(t *testing.T, slots chan struct{}, perPrincipal int) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	v, token := amqpVerifierFixture(t)
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
		done <- ServeAMQP(ctx, l, v, slots, perPrincipal, config.AMQP{Routes: amqpLinkFixture().routes})
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

func amqpClientWrite(t *testing.T, conn net.Conn, p amqpwire.Performative) {
	t.Helper()
	f, err := amqpwire.EncodePerformative(p, 0, 512)
	if err != nil {
		t.Fatal(err)
	}
	if err := amqpwire.WriteFrame(conn, f, 512); err != nil {
		t.Fatal(err)
	}
}

func amqpClientRead(t *testing.T, conn net.Conn) amqpwire.Performative {
	t.Helper()
	for {
		f, err := amqpwire.ReadFrame(conn, 512)
		if err != nil {
			t.Fatal(err)
		}
		p, err := amqpwire.DecodePerformative(f)
		if err != nil {
			t.Fatal(err)
		}
		if p.Kind != 0 {
			return p
		}
	}
}

func waitAMQPSlots(t *testing.T, slots chan struct{}, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(slots) != count {
		if time.Now().After(deadline) {
			t.Fatalf("slots=%d want=%d", len(slots), count)
		}
		time.Sleep(time.Millisecond)
	}
}

func amqpListenerFinished(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("listener did not join")
		return nil
	}
}

func amqpClientNegotiate(t *testing.T, client *tls.Conn, token string) {
	t.Helper()
	amqpClientMechanisms(t, client)
	if err := writeAMQPSASL(client, amqpwire.SASLPerformative{Kind: 0x41, Fields: []amqpwire.Value{{Code: 0xa3, Data: []byte("PLAIN")}, {Code: 0xa0, Data: []byte("\x00producer\x00" + token)}}}); err != nil {
		t.Fatal(err)
	}
	p, err := readAMQPSASL(client)
	if err != nil || p.Kind != 0x44 || p.Fields[0].Number != 0 {
		t.Fatal(p, err)
	}
	amqpClientHeader(t, client, amqpwire.AMQPProtocol)
	write := func(p amqpwire.Performative) {
		f, err := amqpwire.EncodePerformative(p, 0, 512)
		if err != nil {
			t.Fatal(err)
		}
		if err := amqpwire.WriteFrame(client, f, 512); err != nil {
			t.Fatal(err)
		}
	}
	read := func() {
		f, err := amqpwire.ReadFrame(client, 512)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := amqpwire.DecodePerformative(f); err != nil {
			t.Fatal(err)
		}
	}
	write(amqpwire.Performative{Kind: 0x10, Fields: []amqpwire.Value{{Code: 0xa1, Data: []byte("client")}}})
	read()
	write(amqpwire.Performative{Kind: 0x11, Fields: []amqpwire.Value{{Code: 0x40}, {Code: 0x70, Number: 7}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: 8}}})
	read()
}
