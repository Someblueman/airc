//go:build darwin || linux

package gateway_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

type oscoreFixture struct {
	process                *gatewayProcess
	address                string
	config                 config.Config
	credentials            *detachedCredentials
	observer               *irc.Client
	peer, sequence, client string
}

func newOSCOREFixture(t *testing.T) *oscoreFixture {
	t.Helper()
	binary := os.Getenv("AIRC_OSCORE_GATEWAY")
	if binary == "" {
		t.Skip("requires owned sanitizer-enabled native OSCORE worker and pinned client")
	}
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	root := filepath.Dir(c.StateDir)
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}
	credentials := provisionDetached(t, &c)
	contextPath := filepath.Join(root, "oscore.json")
	material, err := auth.CreateOSCOREContext(contextPath, auth.OSCOREOwner{Realm: c.Realm, Listener: "oscore"})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ProvisionOSCORE(t.Context(), store.OSCOREKey{Digest: digest, Listener: "oscore"})
	closeErr := s.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	c.Listeners = []config.Listener{{ID: "oscore", Protocol: "coap_oscore", Enabled: true, Address: "127.0.0.1:0", Operations: ops, OSCORE: &config.OSCORE{Binary: binary, ContextFile: contextPath}}}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := gatewayAddress(t, p)
	observer := dial(t, native, "oscore-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "oscore-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	peer, sequence := filepath.Join(root, "peer.conf"), filepath.Join(root, "peer.seq")
	data, err := material.NativeConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(peer, data, 0600)
	clear(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sequence, []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client := os.Getenv("AIRC_OSCORE_COAP_CLIENT")
	if client == "" {
		client = filepath.Join(filepath.Dir(binary), "libcoap", "coap-client")
	}
	return &oscoreFixture{process: p, address: address, config: c, credentials: credentials, observer: observer, peer: peer, sequence: sequence, client: client}
}
