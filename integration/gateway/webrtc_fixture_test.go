//go:build darwin || linux

package gateway_test

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
)

type webRTCFixture struct {
	process     *gatewayProcess
	address     string
	token       string
	config      config.Config
	credentials *detachedCredentials
	cert        testtls.Certificate
	observer    *irc.Client
}

func newWebRTCFixture(t *testing.T, mode string) webRTCFixture {
	return newWebRTCFixtureWith(t, mode, nil)
}

func newWebRTCFixtureWith(t *testing.T, mode string, configure func(*config.Config)) webRTCFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
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
	c.Principals[0].Operations = ops
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	credentials := provisionDetached(t, &c)
	cert := testtls.NewP256(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	if mode == "" {
		mode = "reliable_ordered"
	}
	c.Listeners = []config.Listener{{ID: "webrtc", Protocol: "webrtc", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops, WebRTC: &config.WebRTC{ICEServers: []config.WebRTCICE{}, ICETransportPolicy: "all"}}}
	if configure != nil {
		configure(&c)
	}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := gatewayAddress(t, p)
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	observer := dial(t, native, "webrtc-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "webrtc-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return webRTCFixture{process: p, address: address, token: token, config: c, credentials: credentials, cert: cert, observer: observer}
}
