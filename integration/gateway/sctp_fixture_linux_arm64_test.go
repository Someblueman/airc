//go:build linux && arm64

package gateway_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type sctpFixture struct {
	process           *gatewayProcess
	address           string
	config            config.Config
	credentials       *detachedCredentials
	observer          *irc.Client
	client            *sctpTestConn
	responseSigning   ed25519.PublicKey
	responseRecipient *ecdsa.PrivateKey
	mode              adapter.SCTPMode
}

func newSCTPFixtureMode(t *testing.T, family string, mode adapter.SCTPMode, check bool) sctpFixture {
	t.Helper()
	if mode != adapter.SCTPOrdered && mode != adapter.SCTPUnordered && mode != adapter.SCTPOrderedTTL {
		t.Fatalf("unsupported SCTP test mode %d", mode)
	}
	listenAddress := "127.0.0.1:0"
	if family == "ipv6" {
		listenAddress = "[::1]:0"
	} else if family != "ipv4" {
		t.Fatalf("unsupported SCTP test family %q", family)
	}

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
	operations := dispatch.ImplementedOperations()
	c.Principals[0].Operations = operations
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	credentials := provisionDetached(t, &c)
	responseSigning, responseRecipient := provisionUDPResponseCredentials(t, &c)
	c.ResponseSigningKey.ID = "sctp-response"
	protocol := "sctp"
	if mode == adapter.SCTPUnordered {
		protocol = "sctp_unordered"
	} else if mode == adapter.SCTPOrderedTTL {
		protocol = "sctp_pr_ttl"
	}
	listener := config.Listener{ID: protocol, Protocol: protocol, Enabled: true, Address: listenAddress, Operations: operations}
	c.Listeners = []config.Listener{listener}
	c.ReturnRoutes = []config.ReturnRoute{{ID: protocol + "-return", Principal: c.Principals[0].ID, Protocol: protocol, Listener: listener.ID, Destination: "connection", RecipientKeyID: "sctp-reader", RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")}}

	process := startGateway(t, gatewayBinary(t), c)
	if check {
		registerGatewayShutdownCheck(t, process)
	}
	address := gatewayAddress(t, process)
	client := dialSCTP(t, address, mode)
	observer := dial(t, native, "sctp-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "sctp-observer" })
	return sctpFixture{process: process, address: address, config: c, credentials: credentials, observer: observer, client: client, responseSigning: responseSigning, responseRecipient: responseRecipient, mode: mode}
}
