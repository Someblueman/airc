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

type nativeQueueFixture struct {
	process                    *gatewayProcess
	address                    string
	responseAddress            string
	kind                       string
	config                     config.Config
	credentials                *detachedCredentials
	observer                   *irc.Client
	source, mailbox            *adapter.MappedQueue
	responseSigning            ed25519.PublicKey
	responseRecipient          *ecdsa.PrivateKey
	lockRoot, ingressDirectory string
}

func newSharedMemoryFixture(t *testing.T) nativeQueueFixture {
	return newNativeQueueFixture(t, "shared_memory", true)
}

func newSharedMemoryFixtureShutdown(t *testing.T, check bool) nativeQueueFixture {
	return newNativeQueueFixture(t, "shared_memory", check)
}

func newMappedFileFixture(t *testing.T) nativeQueueFixture {
	return newNativeQueueFixture(t, "mapped_file", true)
}

func newMappedFileFixtureShutdown(t *testing.T, check bool) nativeQueueFixture {
	return newNativeQueueFixture(t, "mapped_file", check)
}

func newNativeQueueFixture(t *testing.T, kind string, check bool) nativeQueueFixture {
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
	var lockRoot, name, ingressDirectory, responseName, responseDirectory string
	var err error
	if kind == "shared_memory" {
		lockRoot, err = filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(lockRoot, 0700); err != nil {
			t.Fatal(err)
		}
		name, ingressDirectory = provisionSharedMemory(t, lockRoot)
		responseName, responseDirectory = provisionSharedMemory(t, lockRoot)
	} else if kind == "mapped_file" {
		lockRoot = mappedFileFixtureRoot(t)
		ingressDirectory = provisionMappedFileQueue(t, lockRoot)
		responseDirectory = provisionMappedFileQueue(t, lockRoot)
		name, responseName = ingressDirectory, responseDirectory
	} else {
		t.Fatalf("unknown native queue fixture kind %q", kind)
	}
	mailbox, err := openNativeQueue(kind, responseName, responseDirectory, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mailbox.Close(); err != nil {
			t.Error(err)
		}
	})
	listenerID, routeID := kind, kind+"-return"
	listener := config.Listener{ID: listenerID, Protocol: kind, Enabled: true, Address: name, Operations: ops}
	if kind == "shared_memory" {
		listener.SharedMemory = &config.SharedMemory{LockDirectory: lockRoot}
	}
	c.Listeners = []config.Listener{listener}
	c.ReturnRoutes = []config.ReturnRoute{{ID: routeID, Principal: c.Principals[0].ID, Protocol: kind, Listener: listenerID, Destination: responseName, RecipientKeyID: "udp-reader", RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")}}
	p := startGateway(t, gatewayBinary(t), c)
	if check {
		registerGatewayShutdownCheck(t, p)
	}
	if address := gatewayAddress(t, p); address != name {
		t.Fatalf("%s ready address %q want %q", kind, address, name)
	}
	source, err := openNativeQueue(kind, name, ingressDirectory, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	observer := dial(t, native, kind+"-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == kind+"-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return nativeQueueFixture{process: p, address: name, responseAddress: responseName, kind: kind, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox, responseSigning: responseSigning, responseRecipient: responseRecipient, lockRoot: lockRoot, ingressDirectory: ingressDirectory}
}

func openNativeQueue(kind, address, directory string, consumer bool) (*adapter.MappedQueue, error) {
	if kind == "shared_memory" {
		return adapter.OpenSharedMemoryQueue(address, directory, consumer)
	}
	if kind == "mapped_file" {
		return adapter.OpenMappedFileQueue(directory, consumer)
	}
	return nil, adapter.ErrMappedQueueInvalid
}

func provisionNativeQueue(t *testing.T, kind, root string) (address, directory string) {
	t.Helper()
	switch kind {
	case "shared_memory":
		return provisionSharedMemory(t, root)
	case "mapped_file":
		directory = provisionMappedFileQueue(t, root)
		return directory, directory
	default:
		t.Fatalf("unknown native queue fixture kind %q", kind)
		return "", ""
	}
}
