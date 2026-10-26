//go:build darwin || linux

package gateway_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/pion/dtls/v3"
)

const dtlsALPN = adapter.DTLSALPN

type dtlsFixture struct {
	process     *gatewayProcess
	address     string
	token       string
	config      config.Config
	credentials *detachedCredentials
	conn        *dtls.Conn
	cert        testtls.Certificate
	observer    *irc.Client
}

func newDTLSFixture(t *testing.T) dtlsFixture {
	return newDTLSFixtureWithSessionLimit(t, 0)
}

func newDTLSFixtureWithSessionLimit(t *testing.T, sessions int) dtlsFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	if sessions > 0 {
		c.Limits.Connections, c.Limits.Sessions = sessions, sessions // an association is a connection
		c.Limits.Workers = min(c.Limits.Workers, sessions)
		c.Limits.SessionsPerAuthor = min(c.Limits.SessionsPerAuthor, sessions)
		c.Limits.Watches = min(c.Limits.Watches, sessions-1)
		c.Limits.WatchesPerAuthor = min(c.Limits.WatchesPerAuthor, c.Limits.Watches, c.Limits.SessionsPerAuthor-1)
	}
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	credentials := provisionDetached(t, &c)
	cert := testtls.NewP256(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "dtls", Protocol: "dtls", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops, Limits: fixtureListenerLimits()}}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := mcpGatewayAddress(t, p)
	udpAddress, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.DialWithOptions("udp", udpAddress,
		dtls.WithRootCAs(cert.Client.RootCAs),
		dtls.WithServerName("localhost"),
		dtls.WithSupportedProtocols(dtlsALPN),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, dtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384),
		dtls.WithMTU(1200),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	state, ok := conn.ConnectionState()
	if !ok || len(state.PeerCertificates) == 0 ||
		state.NegotiatedProtocol != dtlsALPN ||
		(state.CipherSuiteID != dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 &&
			state.CipherSuiteID != dtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384) {
		_ = conn.Close()
		t.Fatalf("DTLS authenticated cipher/certificate state=%+v ok=%v", state, ok)
	}
	t.Cleanup(func() { _ = conn.Close() })
	observer := dial(t, native, "dtls-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "dtls-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	token := fixtureAuthor + ":" + fixtureAccount
	return dtlsFixture{process: p, address: address, token: token, config: c, credentials: credentials, conn: conn, cert: cert, observer: observer}
}
