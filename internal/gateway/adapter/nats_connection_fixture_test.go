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

func natsConnectionFixture(t *testing.T, verbose bool) (*tls.Conn, *natsConnection, *bufio.Reader) {
	t.Helper()
	v, token := amqpVerifierFixture(t)
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	server, peer := net.Pipe()
	client := tls.Client(peer, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "example.com"})
	ctx, cancel := context.WithCancel(t.Context())
	type result struct {
		c   *natsConnection
		err error
	}
	done := make(chan result, 1)
	finished := make(chan struct{})
	info := natsInfo{ServerID: "fixture-server", ServerName: "fixture", Host: "127.0.0.1", MaxPayload: natswire.MaxPayload, Proto: 1, AuthRequired: true, TLSRequired: true}
	routes := []config.NATSRoute{{Principal: "producer", RequestSubject: "airc.request", ResponseSubject: "airc.response"}}
	go func() {
		defer close(finished)
		c, err := openNATSConnection(ctx, server, &tls.Config{Certificates: []tls.Certificate{pair}}, v, routes, info)
		if err == nil {
			err = c.connected()
			if err != nil {
				c.close()
			}
		}
		done <- result{c, err}
	}()
	t.Cleanup(func() { cancel(); closeMQTTConnection(client); _ = server.Close(); <-finished })
	if err := peer.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	f, err := natswire.Read(bufio.NewReader(peer))
	if err != nil || f.Command != "INFO" {
		t.Fatal("INFO", err)
	}
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	wire := natsConnectBytes(token)
	if verbose {
		wire = []byte(strings.Replace(string(wire), `"verbose":false`, `"verbose":true`, 1))
	}
	if _, err := client.Write(wire); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(client)
	if verbose {
		if f, err := natswire.Read(r); err != nil || f.Command != "+OK" {
			t.Fatal("CONNECT acknowledgement", err)
		}
	}
	var got result
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Fatal("open owner did not join")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	t.Cleanup(got.c.close)
	// The owner's grant snapshot must survive caller mutation.
	routes[0].ResponseSubject = "mutated"
	if got.c.routes[0].ResponseSubject != "airc.response" {
		t.Fatal("route snapshot aliased")
	}
	return client, got.c, r
}
