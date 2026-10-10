//go:build darwin || linux

package gateway_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testfs"
	"github.com/Someblueman/airc/pkg/irc"
)

type unixgramFixture struct {
	process           *gatewayProcess
	address           string
	config            config.Config
	credentials       *detachedCredentials
	observer          *irc.Client
	source, mailbox   *net.UnixConn
	responseSigning   ed25519.PublicKey
	responseRecipient *ecdsa.PrivateKey
}

func newUnixgramFixture(t *testing.T) unixgramFixture {
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
	// A short private directory stays below Darwin's Unix pathname limit.
	root, err := testfs.MkdirTemp("agw-ug-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	listen := func(name string) *net.UnixConn {
		path := filepath.Join(root, name)
		conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		return conn
	}
	source, mailbox := listen("source"), listen("mailbox")
	c.Listeners = []config.Listener{{ID: "unixgram", Protocol: "unixgram", Enabled: true, Address: filepath.Join(root, "gateway"), Operations: ops}}
	c.ReturnRoutes = []config.ReturnRoute{{ID: "unixgram-return", Principal: c.Principals[0].ID, Protocol: "unixgram", Listener: "unixgram", Destination: mailbox.LocalAddr().String(), RecipientKeyID: "udp-reader", RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")}}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	t.Cleanup(func() {
		if t.Failed() {
			_ = p.command.Process.Signal(os.Interrupt)
			select {
			case <-p.done:
				t.Logf("Unix datagram gateway stderr: %s", p.stderr.String())
			case <-time.After(8 * time.Second):
				t.Log("Unix datagram diagnostic process did not join")
			}
		}
	})
	address := mcpGatewayAddress(t, p)
	observer := dial(t, native, "unixgram-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "unixgram-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return unixgramFixture{process: p, address: address, config: c, credentials: credentials, observer: observer, source: source, mailbox: mailbox, responseSigning: responseSigning, responseRecipient: responseRecipient}
}
