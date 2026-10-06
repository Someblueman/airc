//go:build darwin || linux

package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type amqpAuthResult struct {
	identity auth.Identity
	err      error
}

func amqpAuthFixture(t *testing.T, allowed bool) (*tls.Conn, net.Conn, context.CancelFunc, <-chan amqpAuthResult, string, *auth.Verifier) {
	return amqpAuthFixtureVersion(t, allowed, tls.VersionTLS13)
}

func amqpAuthFixtureVersion(t *testing.T, allowed bool, version uint16) (*tls.Conn, net.Conn, context.CancelFunc, <-chan amqpAuthResult, string, *auth.Verifier) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "amqp-auth", filepath.Join(root, "state")
	account, bearer := filepath.Join(root, "account"), filepath.Join(root, "bearer")
	for _, path := range []string{account, bearer} {
		if err := auth.CreateCredential(path); err != nil {
			t.Fatal(err)
		}
	}
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: account}}
	c.Principals = []config.Principal{{ID: "producer", Binding: "native", BearerTokenFile: bearer, Operations: []core.Operation{core.Send}, Targets: []core.Target{{Kind: "room", Name: "#fixture"}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(bearer)
	if err != nil {
		t.Fatal(err)
	}
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	server, peer := net.Pipe()
	secure := tls.Server(server, &tls.Config{MinVersion: version, Certificates: []tls.Certificate{pair}})
	client := tls.Client(peer, &tls.Config{MinVersion: version, MaxVersion: version, RootCAs: roots, ServerName: "example.com"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan amqpAuthResult, 1)
	finished := make(chan struct{})
	principal := "producer"
	if !allowed {
		principal = "other"
	}
	go func() {
		defer close(finished)
		identity, err := authenticateAMQP(ctx, secure, v, []config.AMQPRoute{{Principal: principal}})
		done <- amqpAuthResult{identity, err}
	}()
	t.Cleanup(func() { cancel(); closeMQTTConnection(client); closeMQTTConnection(secure); <-finished })
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := client.HandshakeContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return client, secure, cancel, done, token, v
}

func amqpClientHeader(t *testing.T, conn net.Conn, protocol byte) {
	t.Helper()
	want := []byte{'A', 'M', 'Q', 'P', protocol, 1, 0, 0}
	var wire [8]byte
	if _, err := io.ReadFull(conn, wire[:]); err != nil || !bytes.Equal(wire[:], want) {
		t.Fatal(wire, err)
	}
	if _, err := conn.Write(want); err != nil {
		t.Fatal(err)
	}
}

func amqpClientMechanisms(t *testing.T, conn net.Conn) {
	t.Helper()
	amqpClientHeader(t, conn, amqpwire.SASLProtocol)
	p, err := readAMQPSASL(conn)
	if err != nil || p.Kind != 0x40 || len(p.Fields) != 1 || p.Fields[0].Code != 0xe0 || len(p.Fields[0].Children) != 1 || string(p.Fields[0].Children[0].Data) != "PLAIN" {
		t.Fatal(p, err)
	}
}

func amqpAuthFinished(t *testing.T, done <-chan amqpAuthResult) amqpAuthResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("authentication owner did not join")
		return amqpAuthResult{}
	}
}
