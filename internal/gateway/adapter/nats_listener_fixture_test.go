//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func natsListenerFixture(t *testing.T, slots chan struct{}, perPrincipal int) (net.Listener, func() *tls.Conn, string, context.CancelFunc, <-chan error) {
	t.Helper()
	v, token := amqpVerifierFixture(t)
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- ServeNATS(ctx, l, &tls.Config{Certificates: []tls.Certificate{pair}}, v, slots, perPrincipal, config.NATS{Routes: []config.NATSRoute{{Principal: "producer", RequestSubject: "airc.request", ResponseSubject: "airc.response"}}}, "fixture")
	}()
	t.Cleanup(func() { cancel(); _ = l.Close(); <-finished })
	dial := func() *tls.Conn {
		conn, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		client := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "example.com"})
		t.Cleanup(func() { closeMQTTConnection(client) })
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return client
	}
	return l, dial, token, cancel, done
}

func natsClientConnect(t *testing.T, client *tls.Conn, token string) (*bufio.Reader, natswire.Frame) {
	t.Helper()
	f, err := natswire.Read(bufio.NewReader(client.NetConn()))
	if err != nil || f.Command != "INFO" {
		t.Fatalf("INFO: %v", err)
	}
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	wire := strings.Replace(string(natsConnectBytes(token)), `"verbose":false`, `"verbose":true`, 1)
	if _, err := client.Write([]byte(wire)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(client)
	f, err = natswire.Read(r)
	if err != nil {
		t.Fatal(err)
	}
	return r, f
}
