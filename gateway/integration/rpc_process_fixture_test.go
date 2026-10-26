//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

type rpcHTTPFixture struct {
	process    *gatewayProcess
	address    string
	token      string
	otherToken string
	config     config.Config
	cert       testtls.Certificate
	observer   *irc.Client
	http1      *http.Client
	http2      *http.Client
}

func newRPCHTTPFixture(t *testing.T, http2 bool) rpcHTTPFixture {
	t.Helper()
	_ = http2
	native := fixtureWithAdmin(t, true)
	observer := dial(t, native, "rpc-fixture-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "rpc-fixture-observer" })
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
	cert := testtls.New(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "rpc", Protocol: "http", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops, Limits: fixtureListenerLimits()}}
	p := startGateway(t, gatewayBinary(t), c)
	t.Cleanup(func() { _ = observer.Close() })
	token := fixtureAuthor + ":" + fixtureAccount
	otherToken := "fixture-other"
	http1Transport := &http.Transport{TLSClientConfig: cert.Client}
	http2Transport := &http.Transport{TLSClientConfig: cert.Client, ForceAttemptHTTP2: true}
	client1, client2 := &http.Client{Transport: http1Transport}, &http.Client{Transport: http2Transport}
	t.Cleanup(func() { http1Transport.CloseIdleConnections(); http2Transport.CloseIdleConnections() })
	return rpcHTTPFixture{process: p, address: mcpGatewayAddress(t, p), token: token, otherToken: otherToken, config: c, cert: cert, observer: observer, http1: client1, http2: client2}
}
