//go:build linux && arm64

package gateway_test

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type sctpFixture struct {
	process     *gatewayProcess
	address     string
	config      config.Config
	credentials *detachedCredentials
	observer    *irc.Client
	client      *sctpTestConn
	mode        adapter.SCTPMode
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
	credentials := provisionDetached(t, &c)
	protocol := "sctp"
	if mode == adapter.SCTPUnordered {
		protocol = "sctp_unordered"
	} else if mode == adapter.SCTPOrderedTTL {
		protocol = "sctp_pr_ttl"
	}
	listener := config.Listener{ID: protocol, Protocol: protocol, Enabled: true, Address: listenAddress, Operations: operations}
	c.Listeners = []config.Listener{listener}

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
	return sctpFixture{process: process, address: address, config: c, credentials: credentials, observer: observer, client: client, mode: mode}
}
