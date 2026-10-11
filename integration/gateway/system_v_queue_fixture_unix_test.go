//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package gateway_test

import (
	"crypto/rand"
	"encoding/binary"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/sysvqueue"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

type systemVFixture struct {
	process     *gatewayProcess
	address     string
	config      config.Config
	credentials *detachedCredentials
	observer    *irc.Client
	// mailbox is the listener's provisioned reply_to queue.
	source, mailbox *sysvqueue.Queue
}

func newSystemVFixture(t *testing.T) systemVFixture {
	return newSystemVFixtureShutdown(t, true)
}

func newSystemVFixtureShutdown(t *testing.T, check bool) systemVFixture {
	return newSystemVFixtureBinding(t, check, nil)
}

func newSystemVFixtureBinding(t *testing.T, check bool, binding func(*config.Config)) systemVFixture {
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
	ingressKey := freeSystemVKey(t)
	responseKey := freeSystemVKey(t)
	if ingressKey == responseKey {
		t.Fatal("fixture queue key collision")
	}
	address := strconv.FormatInt(int64(ingressKey), 10)
	mailbox, err := sysvqueue.Create(responseKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mailbox.Close() })
	c.Listeners = []config.Listener{{ID: "systemV", Protocol: "system_v_queue", Enabled: true, Address: address, ReplyTo: strconv.FormatInt(int64(responseKey), 10), Operations: ops}}
	if binding != nil {
		binding(&c)
	}
	p := startGateway(t, gatewayBinary(t), c)
	if check {
		registerGatewayShutdownCheck(t, p)
	}
	address = gatewayAddress(t, p)
	source, err := sysvqueue.Open(ingressKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	observer := dial(t, native, "systemV-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "systemV-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return systemVFixture{process: p, address: address, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox}
}

func freeSystemVKey(t *testing.T) int32 {
	t.Helper()
	for range 16 {
		var bytes [4]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			t.Fatal(err)
		}
		key := int32(binary.LittleEndian.Uint32(bytes[:]) & 0x7fffffff)
		if key == 0 {
			continue
		}
		q, err := sysvqueue.Create(key)
		if err != nil {
			continue
		}
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		return key
	}
	t.Fatal("could not allocate unused fixture queue key")
	return 0
}
