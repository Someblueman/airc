//go:build darwin || linux

package gateway_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"net"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type udpFixture struct {
	process           *gatewayProcess
	address           string
	config            config.Config
	credentials       *detachedCredentials
	observer          *irc.Client
	source            *net.UDPConn
	mailbox           *net.UDPConn
	responseSigning   ed25519.PublicKey
	responseRecipient *ecdsa.PrivateKey
}

func newUDPFixture(t *testing.T) udpFixture {
	return newPacketFixture(t, "udp", nil)
}

func newPacketFixture(t *testing.T, protocol string, configure func(*config.ReturnRoute, *net.UDPConn)) udpFixture {
	t.Helper()
	return newPacketFixtureConfigured(t, protocol, func(c *config.Config, _ *net.UDPConn, mailbox *net.UDPConn) {
		if configure != nil {
			configure(&c.ReturnRoutes[0], mailbox)
		}
	})
}

func newPacketFixtureConfigured(t *testing.T, protocol string, configure func(*config.Config, *net.UDPConn, *net.UDPConn)) udpFixture {
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
	responseSigning, responseRecipient := provisionUDPResponseCredentials(t, &c)
	source, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close(); _ = mailbox.Close() })
	mailboxAddr := mailbox.LocalAddr().(*net.UDPAddr)
	c.Listeners = []config.Listener{{ID: "udp", Protocol: protocol, Enabled: true, Address: "127.0.0.1:0", Operations: ops}}
	c.ReturnRoutes = []config.ReturnRoute{{ID: "udp-return", Principal: c.Principals[0].ID, Protocol: protocol, Listener: "udp", Destination: mailboxAddr.String(), RecipientKeyID: "udp-reader", RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")}}
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
	return udpFixture{process: p, address: address, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox, responseSigning: responseSigning, responseRecipient: responseRecipient}
}
