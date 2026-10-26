//go:build darwin || linux

package gateway_test

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type udpFixture struct {
	process     *gatewayProcess
	address     string
	config      config.Config
	credentials *detachedCredentials
	observer    *irc.Client
	source      *net.UDPConn
	// mailbox receives the gateway's replies: the source itself, as plain
	// author replies go back to the request's source address.
	mailbox *net.UDPConn
}

func newUDPFixture(t *testing.T) udpFixture {
	return newPacketFixture(t, "udp", nil)
}

func newPacketFixture(t *testing.T, protocol string, configure func(*config.Listener)) udpFixture {
	t.Helper()
	return newPacketFixtureConfigured(t, protocol, func(c *config.Config, _ *net.UDPConn, _ *net.UDPConn) {
		if configure != nil {
			configure(&c.Listeners[0])
		}
	}, listenUDPFixtureSocket)
}

func newPacketFixtureConfigured(t *testing.T, protocol string, configure func(*config.Config, *net.UDPConn, *net.UDPConn), listen func() (*net.UDPConn, error)) udpFixture {
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
	credentials := provisionDetached(t, &c)
	source, err := listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	mailbox := source
	c.Listeners = []config.Listener{{ID: "udp", Protocol: protocol, Enabled: true, Address: "127.0.0.1:0", Operations: ops, Limits: fixtureListenerLimits()}}
	if configure != nil {
		configure(&c, source, mailbox)
	}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := mcpGatewayAddress(t, p)
	observer := dial(t, native, "udp-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "udp-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return udpFixture{process: p, address: address, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox}
}

func listenUDPFixtureSocket() (*net.UDPConn, error) {
	return net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
}
