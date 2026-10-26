package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// tunnelFixture is a daemon reached natively through a proxy, and a gateway
// serving its two native tunnels, which the daemon advertises.
type tunnelFixture struct {
	daemon string
	proxy  *nativeProxy
	ports  map[string]int
}

func newTunnelFixture(t *testing.T) *tunnelFixture {
	t.Helper()
	agentEnv(t)
	t.Setenv("AIRC_CARRIERS", "")
	f := &tunnelFixture{ports: map[string]int{}}
	native := closedTCPPort(t)
	carriers := []protocol.Carrier{{Name: "irc", Port: native}}
	var listeners []gatewayListener
	for _, name := range []string{"irc_websocket", "irc_connect"} {
		f.ports[name] = closedTCPPort(t)
		carriers = append(carriers, protocol.Carrier{Name: name, Port: f.ports[name]})
		listeners = append(listeners, gatewayListener{ID: name, Protocol: name, Address: "127.0.0.1:" + strconv.Itoa(f.ports[name]), Enabled: true})
	}
	dir := t.TempDir()
	accounts, tokenFile := filepath.Join(dir, "accounts"), filepath.Join(dir, "gateway.token")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	createGatewayCredential(t, tokenFile)
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	f.daemon = cliTestServerSetup(t, server.Config{HistoryLimit: 64, Carriers: carriers}, func(s *server.Server) error {
		if err := s.EnableGateway(strings.TrimSpace(string(token))); err != nil {
			return err
		}
		return s.RestoreAccounts(accounts)
	})
	f.proxy = startNativeProxy(t, "127.0.0.1:"+strconv.Itoa(native), f.daemon)
	startGatewayUpstream(t, gatewayUpstream{Network: "tcp", Address: f.daemon, GatewayTokenFile: tokenFile}, listeners, carriers)
	return f
}

func (f *tunnelFixture) carrier(name string) string { return name + ":" + strconv.Itoa(f.ports[name]) }

// With native IRC unreachable, a native tunnel carries the whole native
// feature set: a one-shot send reports the tunnel, and a command that needs
// native IRC, unread, runs through it too.
func TestNativeTunnelsCarryNativeOnlyCommands(t *testing.T) {
	f := newTunnelFixture(t)
	// First contact learns the carriers, native tunnels included.
	if _, stderr, err := sendJSON(t, "--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops", "--message", "natively"); err != nil {
		t.Fatal(err, stderr)
	}
	if learned := loadCarrierCache(options{addr: f.proxy.addr()}); len(learned) != 3 || learned[1].Name != "irc_websocket" || learned[2].Name != "irc_connect" {
		t.Fatalf("learned %v", learned)
	}
	bob, err := irc.Dial(irc.Config{Nick: "bob", Addr: f.daemon, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	if err := bob.Send("alice", "are you there?"); err != nil {
		t.Fatal(err)
	}
	f.proxy.down()

	// The learned tunnel answers a send, which says so.
	sent, stderr, err := sendJSON(t, "--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops", "--message", "through the tunnel", "--request-id", "nt-1")
	if err != nil || sent.Carrier != f.carrier("irc_websocket") || sent.From != "alice" || !protocol.ValidMessageID(sent.ID) {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	if got := historyOf(t, f.daemon, "#ops", "through the tunnel"); len(got) != 1 {
		t.Fatalf("tunnelled post: %+v", got)
	}
	again, _, err := sendJSON(t, "--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops", "--message", "through the tunnel", "--request-id", "nt-1")
	if err != nil || again.ID != sent.ID {
		t.Fatal("retry through the tunnel posted again", again, err)
	}

	// unread needs native IRC; the other tunnel, named explicitly, carries it.
	var stdout, errOut bytes.Buffer
	if err := run([]string{"unread", "--addr", f.proxy.addr(), "--nick", "alice", "--mentions", "--carriers", f.carrier("irc_connect")}, strings.NewReader(""), &stdout, &errOut); err != nil {
		t.Fatalf("unread through the tunnel: %v %s", err, errOut.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("unread through the tunnel saw no waiting direct message")
	}
}

// A registered user logs in with SASL inside the tunnel, as on aircd's own
// port, and posts as its account.
func TestNativeTunnelCarriesTheAccountLogin(t *testing.T) {
	f := newTunnelFixture(t)
	if err := run([]string{"user", "create", "--addr", f.proxy.addr(), "--nick", "carol"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	f.proxy.down()
	sent, stderr, err := sendJSON(t, "--addr", f.proxy.addr(), "--nick", "carol", "--channel", "#ops", "--message", "registered", "--carriers", f.carrier("irc_connect"))
	if err != nil || sent.Carrier != f.carrier("irc_connect") || sent.From != "carol" {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	if got := historyOf(t, f.daemon, "#ops", "registered"); len(got) != 1 || got[0].AccountID == "" {
		t.Fatalf("registered post: %+v", got)
	}
	t.Setenv("AIRC_STATE_DIR", t.TempDir()) // no saved identity: a guest on a registered nick is refused by aircd itself
	_, stderr, err = sendJSON(t, "--addr", f.proxy.addr(), "--nick", "carol", "--channel", "#ops", "--message", "impostor", "--carriers", f.carrier("irc_connect"))
	if err == nil || !strings.Contains(stderr, `"code":"auth_failed"`) {
		t.Fatalf("guest on a registered nick: %v %s", err, stderr)
	}
}
