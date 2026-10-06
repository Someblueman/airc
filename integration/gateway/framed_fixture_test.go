//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
)

func framedFixture(t *testing.T, secure bool) (net.Conn, *detachedCredentials, string, func()) {
	t.Helper()
	return framedFixtureAddress(t, secure, "127.0.0.1:0")
}

func framedFixtureAddress(t *testing.T, secure bool, listenAddress string) (net.Conn, *detachedCredentials, string, func()) {
	t.Helper()
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, socket, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, account)
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 2, 1
	listener := config.Listener{ID: "stream", Protocol: "framed", Address: listenAddress, Enabled: true, Operations: dispatch.ImplementedOperations()}
	certificate := testtls.New(t)
	if secure {
		listener.TLSCertFile, listener.TLSKeyFile = certificate.CertPath, certificate.KeyPath
	}
	c.Listeners = []config.Listener{listener}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	var conn net.Conn
	var err error
	if secure {
		for _, bad := range []*tls.Config{
			{MinVersion: tls.VersionTLS13, NextProtos: []string{adapter.FramedALPN}},
			{MinVersion: tls.VersionTLS13, RootCAs: certificate.Client.RootCAs, ServerName: "wrong.example", NextProtos: []string{adapter.FramedALPN}},
		} {
			rejected, e := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, bad)
			if e == nil {
				_ = rejected.Close()
				t.Fatal("TLS accepted untrusted certificate/name")
			}
		}
		trust := certificate.Client.Clone()
		trust.NextProtos = []string{adapter.FramedALPN}
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, trust)
	} else {
		conn, err = net.DialTimeout("tcp", address, 3*time.Second)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	exampleCA := ""
	if secure {
		exampleCA = certificate.CAPath
	}
	framedExample(t, address, exampleCA, credentials)
	stop := func() {
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err, p.stderr.String())
			}
		case <-time.After(8 * time.Second):
			t.Fatal("framed CLI did not join")
		}
	}
	return conn, credentials, socket, stop
}

func writeFrame(t *testing.T, conn adapter.FramedStream, r adapter.FrameRequest) {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err = io.Copy(conn, io.MultiReader(bytes.NewReader(header[:]), bytes.NewReader(body))); err != nil {
		t.Fatal(err)
	}
}

func readFrame(t *testing.T, conn adapter.FramedStream) adapter.FrameResponse {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > adapter.MaxResponseFrameBytes {
		t.Fatal("unbounded response", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	var r adapter.FrameResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 || r.CorrelationID == "" {
		t.Fatal("uncorrelated response", r)
	}
	return r
}
