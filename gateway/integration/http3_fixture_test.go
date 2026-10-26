//go:build darwin || linux

package gateway_test

import (
	"crypto/tls"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/quic-go/quic-go/http3"
)

type http3GatewayFixture struct {
	process     *gatewayProcess
	address     string
	token       string
	otherToken  string
	config      config.Config
	cert        testtls.Certificate
	observer    *irc.Client
	credentials *detachedCredentials
	client      *http.Client
	transport   *http3.Transport
}

func newHTTP3GatewayFixture(t *testing.T) http3GatewayFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	observer := dial(t, native, "http3-fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "http3-fixture-observer" })
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	credentials := provisionDetached(t, &c)
	cert := testtls.New(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "http3", Protocol: "http3", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops, Limits: fixtureListenerLimits()}}
	p := startGateway(t, gatewayBinary(t), c)
	t.Cleanup(func() { _ = observer.Close() })
	token := fixtureAuthor + ":" + fixtureAccount
	otherToken := "fixture-other"
	address := mcpGatewayAddress(t, p)
	tlsConfig := cert.Client.Clone()
	tlsConfig.MinVersion = tls.VersionTLS13
	transport := &http3.Transport{TLSClientConfig: tlsConfig}
	client := &http.Client{Transport: transport}
	t.Cleanup(func() { _ = transport.Close() })
	return http3GatewayFixture{process: p, address: address, token: token, otherToken: otherToken, config: c, cert: cert, observer: observer, credentials: credentials, client: client, transport: transport}
}
