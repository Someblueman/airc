//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type posixFixture struct {
	process     *gatewayProcess
	address     string
	config      config.Config
	credentials *detachedCredentials
	observer    *irc.Client
	// mailbox is the listener's provisioned reply_to queue.
	source, mailbox *adapter.POSIXQueue
}

func newPOSIXFixture(t *testing.T) posixFixture {
	return newPOSIXFixtureShutdown(t, true)
}

func newPOSIXFixtureShutdown(t *testing.T, check bool) posixFixture {
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
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	address := fmt.Sprintf("/agw-native-%x", nonce)
	mailbox, err := adapter.CreatePOSIXQueue(address+"-reply", 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mailbox.Close() })
	c.Listeners = []config.Listener{{ID: "posix", Protocol: "posix_mqueue", Enabled: true, Address: address, Operations: ops, ReplyTo: mailbox.Name(), POSIXQueue: &config.POSIXQueue{Depth: 8}}}
	p := startGateway(t, gatewayBinary(t), c)
	if check {
		registerGatewayShutdownCheck(t, p)
	}
	address = gatewayAddress(t, p)
	source, err := adapter.OpenPOSIXQueue(address, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	observer := dial(t, native, "posix-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "posix-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return posixFixture{process: p, address: address, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox}
}
