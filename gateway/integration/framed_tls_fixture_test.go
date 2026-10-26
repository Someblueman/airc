//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
)

// framedTLSCardFixture is a gateway with one TLS framed listener, connected.
func framedTLSCardFixture(t *testing.T) (net.Conn, *detachedCredentials, func(), string) {
	t.Helper()
	socket := fixture(t)
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	certificate := testtls.New(t)
	listener := config.Listener{ID: "stream", Protocol: "framed", Address: "127.0.0.1:0", Enabled: true, Operations: dispatch.ImplementedOperations(), TLSCertFile: certificate.CertPath, TLSKeyFile: certificate.KeyPath}
	c.Listeners = []config.Listener{listener}
	p := startGateway(t, gatewayBinary(t), c)
	address := gatewayAddress(t, p)
	trust := certificate.Client.Clone()
	trust.NextProtos = []string{adapter.FramedALPN}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", address, trust)
	if err != nil {
		t.Fatal(err)
	}
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
			t.Fatal("TLS gateway did not join")
		}
	}
	return conn, credentials, stop, socket
}
