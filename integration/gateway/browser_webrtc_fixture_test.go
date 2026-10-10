//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type browserWebRTCFixture struct {
	base        string
	socket      string
	bearer      string
	credentials *detachedCredentials
	observer    *irc.Client
}

func newBrowserWebRTCFixture(t *testing.T, origin string) browserWebRTCFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Principals[0].Operations = dispatch.ImplementedOperations()
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	credentials := provisionDetached(t, &c)
	cert := trustedBrowserCertificate(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	c.Listeners = []config.Listener{{ID: "webrtc", Protocol: "webrtc", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, AllowedOrigins: []string{origin}, Operations: ops, WebRTC: &config.WebRTC{ICEServers: []config.WebRTCICE{}, ICETransportPolicy: "all"}}}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := gatewayAddress(t, p)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	observer, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: native, Nick: "browser-webrtc-observer", ReadTimeout: 25 * time.Minute, WriteTimeout: time.Second})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "browser-webrtc-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	bearer, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	return browserWebRTCFixture{base: "https://" + address + "/v1/webrtc/offer", socket: native, bearer: bearer, credentials: credentials, observer: observer}
}

func (f browserWebRTCFixture) waitBrowser(t *testing.T, reports <-chan string) browserWebRTCReport {
	t.Helper()
	select {
	case report := <-reports:
		var evidence browserWebRTCReport
		if err := json.Unmarshal([]byte(report), &evidence); err != nil || evidence.Status != "PASS" {
			t.Fatal(report)
		}
		return evidence
	case <-time.After(20 * time.Minute):
		t.Fatal("browser WebRTC probe timed out")
	}
	return browserWebRTCReport{}
}
