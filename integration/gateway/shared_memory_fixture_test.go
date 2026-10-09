//go:build (darwin || linux) && arm64

package gateway_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type sharedMemoryFixture struct {
	process                    *gatewayProcess
	address                    string
	config                     config.Config
	credentials                *detachedCredentials
	observer                   *irc.Client
	source, mailbox            *adapter.SharedMemoryQueue
	responseSigning            ed25519.PublicKey
	responseRecipient          *ecdsa.PrivateKey
	lockRoot, ingressDirectory string
}

func newSharedMemoryFixture(t *testing.T) sharedMemoryFixture {
	return newSharedMemoryFixtureShutdown(t, true)
}

func newSharedMemoryFixtureShutdown(t *testing.T, check bool) sharedMemoryFixture {
	t.Helper()
	native := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead, core.Watch}
	c.Principals[0].Operations = ops
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}}
	credentials := provisionDetached(t, &c)
	c.Principals[0].BearerTokenFile = ""
	responseSigning, responseRecipient := provisionUDPResponseCredentials(t, &c)
	lockRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockRoot, 0700); err != nil {
		t.Fatal(err)
	}
	name, ingressDirectory := provisionSharedMemory(t, lockRoot)
	responseName, responseDirectory := provisionSharedMemory(t, lockRoot)
	mailbox, err := adapter.OpenSharedMemoryQueue(responseName, responseDirectory, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mailbox.Close(); err != nil {
			t.Error(err)
		}
	})
	c.Listeners = []config.Listener{{ID: "shared-memory", Protocol: "shared_memory", Enabled: true, Address: name, SharedMemory: &config.SharedMemory{LockDirectory: lockRoot}, Operations: ops}}
	c.ReturnRoutes = []config.ReturnRoute{{ID: "shared-memory-return", Principal: c.Principals[0].ID, Protocol: "shared_memory", Listener: "shared-memory", Destination: responseName, RecipientKeyID: "udp-reader", RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")}}
	p := startGateway(t, gatewayBinary(t), c)
	if check {
		registerGatewayShutdownCheck(t, p)
	}
	if address := gatewayAddress(t, p); address != name {
		t.Fatal("shared-memory ready address", address)
	}
	source, err := adapter.OpenSharedMemoryQueue(name, ingressDirectory, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	observer := dial(t, native, "shared-memory-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "shared-memory-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return sharedMemoryFixture{process: p, address: name, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox, responseSigning: responseSigning, responseRecipient: responseRecipient, lockRoot: lockRoot, ingressDirectory: ingressDirectory}
}
