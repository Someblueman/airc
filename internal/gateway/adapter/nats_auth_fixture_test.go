//go:build darwin || linux

package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

type natsAuthResult struct {
	session natsAuthenticated
	err     error
}

func natsAuthFixture(t *testing.T, version uint16) (*tls.Conn, context.CancelFunc, <-chan natsAuthResult, string) {
	t.Helper()
	v, token := amqpVerifierFixture(t)
	cert := httptest.NewTLSServer(nil)
	pair, leaf := cert.TLS.Certificates[0], cert.Certificate()
	cert.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	server, peer := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan natsAuthResult, 1)
	finished := make(chan struct{})
	info := natsInfo{ServerID: "fixture-server", ServerName: "fixture", Version: "1", Go: "go1.27", Host: "127.0.0.1", MaxPayload: natswire.MaxPayload, Proto: 1, AuthRequired: true, TLSRequired: true}
	go func() {
		defer close(finished)
		s, err := authenticateNATS(ctx, server, &tls.Config{Certificates: []tls.Certificate{pair}}, v, []config.NATSRoute{{Principal: "producer"}}, info)
		done <- natsAuthResult{s, err}
	}()
	client := tls.Client(peer, &tls.Config{MinVersion: version, MaxVersion: version, RootCAs: roots, ServerName: "example.com"})
	t.Cleanup(func() { cancel(); closeMQTTConnection(client); _ = server.Close(); <-finished })
	if err := peer.SetDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	f, err := natswire.Read(bufio.NewReader(peer))
	if err != nil || f.Command != "INFO" {
		t.Fatalf("initial INFO: %v", err)
	}
	var advertised natsInfo
	if json.Unmarshal(f.JSON, &advertised) != nil || advertised != info {
		t.Fatal("incorrect INFO")
	}
	return client, cancel, done, token
}

func natsAuthFinished(t *testing.T, done <-chan natsAuthResult) natsAuthResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("authentication did not join")
		return natsAuthResult{}
	}
}

func natsConnectBytes(token string) []byte {
	return []byte(`CONNECT {"verbose":false,"pedantic":false,"tls_required":true,"auth_token":"` + token + `","name":"fixture","lang":"go","version":"1","protocol":1,"echo":true,"headers":false,"no_responders":false}` + "\r\n")
}
