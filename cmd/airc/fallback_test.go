//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// nativeProxy stands in for aircd's native port: it forwards to the daemon
// until the test takes it down. With blackhole set it forwards the first
// PRIVMSG and then goes silent and stops accepting, as when a connection
// dies after a send landed. observes counts the OBSERVE commands it
// forwarded: native waits and watches beginning.
type nativeProxy struct {
	listener  net.Listener
	upstream  string
	blackhole bool
	frozen    atomic.Bool // every connection silent, as when a link dies
	observes  atomic.Int32
	mu        sync.Mutex
	conns     []net.Conn
	closed    bool
}

// freeze stops accepting and makes every open connection silent both ways
// without closing it.
func (p *nativeProxy) freeze() {
	p.frozen.Store(true)
	p.down()
}

func startNativeProxy(t *testing.T, address, upstream string) *nativeProxy {
	t.Helper()
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	p := &nativeProxy{listener: l, upstream: upstream}
	go p.serve()
	t.Cleanup(p.close)
	return p
}

func (p *nativeProxy) setBlackhole() {
	p.mu.Lock()
	p.blackhole = true
	p.mu.Unlock()
}

// close ends every proxied connection, silent or not.
func (p *nativeProxy) close() {
	p.down()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

func (p *nativeProxy) addr() string { return p.listener.Addr().String() }

func (p *nativeProxy) port() int { return p.listener.Addr().(*net.TCPAddr).Port }

func (p *nativeProxy) serve() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.upstream)
		if err != nil {
			_ = client.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, client, server)
		blackhole := p.blackhole
		p.mu.Unlock()
		var silent sync.Once
		quiet := make(chan struct{})
		go func() { // client -> server, line by line
			reader := bufio.NewReader(client)
			for {
				line, err := reader.ReadString('\n')
				if p.frozen.Load() {
					line = ""
				}
				if blackhole && strings.Contains(line, "PRIVMSG ") {
					silent.Do(func() { close(quiet); p.down() })
				}
				if line != "" {
					if _, werr := io.WriteString(server, line); werr != nil {
						return
					}
					if strings.HasPrefix(line, "OBSERVE ") {
						p.observes.Add(1)
					}
				}
				if err != nil {
					return
				}
			}
		}()
		go func() { // server -> client, until the connection goes silent
			buf := make([]byte, 4096)
			for {
				n, err := server.Read(buf)
				select {
				case <-quiet:
					continue // the receipt never arrives
				default:
				}
				if p.frozen.Load() {
					continue
				}
				if n > 0 {
					if _, werr := client.Write(buf[:n]); werr != nil {
						return
					}
				}
				if err != nil {
					_ = client.Close()
					return
				}
			}
		}()
	}
}

func (p *nativeProxy) isDown() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// down stops accepting; existing connections close unless one went silent.
func (p *nativeProxy) down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	_ = p.listener.Close()
	if !p.blackhole && !p.frozen.Load() {
		for _, c := range p.conns {
			_ = c.Close()
		}
	}
}

// fallbackFixture is a daemon reached natively through a proxy, and its
// gateway reached directly (over TCP carriers and its Unix socket),
// advertising the proxy as aircd's own port.
type fallbackFixture struct {
	daemon string
	proxy  *nativeProxy
	ports  map[string]int
	sock   string // the gateway's Unix socket, not advertised
	log    *observeLog
}

// observeLog counts the daemon's channel_observed records by nick and
// channel, natively or through a gateway session, so a test can wait for a
// reader to be waiting.
type observeLog struct {
	mu sync.Mutex
	n  map[string]int
}

func (o *observeLog) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}
func (o *observeLog) WithAttrs([]slog.Attr) slog.Handler { return o }
func (o *observeLog) WithGroup(string) slog.Handler      { return o }

func (o *observeLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "channel_observed" {
		return nil
	}
	var nick, channel string
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "nick":
			nick = strings.ToLower(a.Value.String())
		case "channel":
			channel = a.Value.String()
		}
		return true
	})
	o.mu.Lock()
	defer o.mu.Unlock()
	o.n[nick+" "+channel]++
	return nil
}

// observed is how often nick has begun observing channel on the daemon.
func (f *fallbackFixture) observed(nick, channel string) int {
	f.log.mu.Lock()
	defer f.log.mu.Unlock()
	return f.log.n[strings.ToLower(nick)+" "+channel]
}

func newFallbackFixture(t *testing.T) *fallbackFixture {
	t.Helper()
	agentEnv(t)
	t.Setenv("AIRC_CARRIERS", "")
	f := &fallbackFixture{ports: map[string]int{}, log: &observeLog{n: map[string]int{}}}
	native := closedTCPPort(t)
	for _, name := range []string{"http", "websocket", "framed"} {
		f.ports[name] = closedTCPPort(t)
	}
	carriers := []protocol.Carrier{{Name: "irc", Port: native}, {Name: "http", Port: f.ports["http"]}, {Name: "websocket", Port: f.ports["websocket"]}}
	accounts := filepath.Join(t.TempDir(), "accounts")
	f.daemon = cliTestServerSetup(t, server.Config{HistoryLimit: 64, Carriers: carriers, Logger: slog.New(f.log)}, func(s *server.Server) error { return s.RestoreAccounts(accounts) })
	f.proxy = startNativeProxy(t, "127.0.0.1:"+strconv.Itoa(native), f.daemon)
	dir, err := os.MkdirTemp("", "agw") // a socket path must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f.sock = filepath.Join(dir, "gateway.sock")
	listeners := []gatewayListener{{ID: "unix", Protocol: "unix", Address: f.sock, Enabled: true}}
	for name, port := range f.ports {
		listeners = append(listeners, gatewayListener{ID: name, Protocol: name, Address: "127.0.0.1:" + strconv.Itoa(port), Enabled: true})
	}
	startGatewayListeners(t, f.daemon, listeners, carriers)
	return f
}

func (f *fallbackFixture) carrier(name string) string {
	if name == "unix" {
		return "unix:" + f.sock
	}
	return name + ":" + strconv.Itoa(f.ports[name])
}

// historyOf counts body in the daemon's retained history of room.
func historyOf(t *testing.T, daemon, room, body string) []*irc.HistoryEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, irc.Config{Nick: "auditor", Addr: daemon, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	page, err := fetchHistory(ctx, client, room, "", 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []*irc.HistoryEvent
	for _, m := range page.messages {
		if m.Message == body {
			out = append(out, m)
		}
	}
	return out
}

func sendJSON(t *testing.T, args ...string) (sendResult, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(append([]string{"send", "--json"}, args...), strings.NewReader(""), &stdout, &stderr)
	var result sendResult
	if err == nil {
		if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
			t.Fatalf("send output %q: %v", stdout.String(), decodeErr)
		}
	}
	return result, stderr.String(), err
}

// With native IRC down, send posts over the gateway's carriers in order,
// once, as the right author; history reads the room the same way.
func TestSendAndHistoryFallBackWhenNativeIRCIsUnreachable(t *testing.T) {
	f := newFallbackFixture(t)
	closed := strconv.Itoa(f.proxy.port())
	f.proxy.down()
	common := []string{"--addr", "127.0.0.1:" + closed, "--nick", "alice", "--channel", "#ops"}

	sent, stderr, err := sendJSON(t, append(common, "--carriers", f.carrier("http"), "--message", "over http", "--request-id", "fb-1")...)
	if err != nil || sent.Carrier != f.carrier("http") || !protocol.ValidMessageID(sent.ID) || sent.From != "alice" || sent.RequestID != "fb-1" || !strings.HasPrefix(sent.Code, "accepted") {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	// First contact through --carriers saved the list the gateway advertises.
	if learned := loadCarrierCache(options{addr: "127.0.0.1:" + closed}); len(learned) != 3 || learned[1].String() != f.carrier("http") {
		t.Fatalf("learned %v", learned)
	}
	again, _, err := sendJSON(t, append(common, "--carriers", f.carrier("http"), "--message", "over http", "--request-id", "fb-1")...)
	if err != nil || again.ID != sent.ID {
		t.Fatal("retry over the same carrier posted again", again, err)
	}
	// HTTP is down too: the next carrier answers. Another machine (no local
	// outbox) retrying the same request ID still gets the one message.
	t.Setenv("AIRC_STATE_DIR", t.TempDir())
	down := "http:" + strconv.Itoa(closedTCPPort(t))
	viaWS, stderr, err := sendJSON(t, append(common, "--carriers", down+","+f.carrier("websocket"), "--message", "over http", "--request-id", "fb-1")...)
	if err != nil || viaWS.ID != sent.ID || viaWS.Carrier != f.carrier("websocket") {
		t.Fatalf("websocket retry: %+v %v %s", viaWS, err, stderr)
	}
	framed, stderr, err := sendJSON(t, append(common, "--carriers", down+","+f.carrier("framed"), "--message", "over framed")...)
	if err != nil || framed.Carrier != f.carrier("framed") {
		t.Fatalf("framed: %+v %v %s", framed, err, stderr)
	}
	if got := historyOf(t, f.daemon, "#ops", "over http"); len(got) != 1 || got[0].From != "alice" || got[0].ID != sent.ID {
		t.Fatalf("history holds %d copies: %+v", len(got), got)
	}
	if got := historyOf(t, f.daemon, "#ops", "over framed"); len(got) != 1 || got[0].From != "alice" {
		t.Fatalf("framed post: %+v", got)
	}
	// A direct message falls back too.
	dm, stderr, err := sendJSON(t, "--addr", "127.0.0.1:"+closed, "--nick", "alice", "--to", "bob", "--carriers", f.carrier("http"), "--message", "psst")
	if err != nil || dm.Target != "bob" || dm.Delivered == nil || *dm.Delivered {
		t.Fatalf("dm: %+v %v %s", dm, err, stderr)
	}

	var stdout bytes.Buffer
	if err := run([]string{"history", "#ops", "--addr", "127.0.0.1:" + closed, "--carriers", f.carrier("http"), "--json"}, nil, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(stdout.String()), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"over http"`) || !strings.Contains(lines[1], `"over framed"`) {
		t.Fatalf("history over the gateway: %s", stdout.String())
	}

	// Every carrier down: nothing was sent, so nothing waits in the outbox.
	_, stderr, err = sendJSON(t, append(common, "--carriers", down, "--message", "nowhere")...)
	if err == nil || !strings.Contains(stderr, `"code":"server_unavailable"`) || !strings.Contains(stderr, `"retryable":true`) || !strings.Contains(stderr, "fallback") {
		t.Fatalf("all carriers down: %v %s", err, stderr)
	}
	var pending bytes.Buffer
	if err := run([]string{"send", "--pending", "--json", "--addr", "127.0.0.1:" + closed, "--nick", "alice"}, nil, &pending, io.Discard); err != nil || strings.TrimSpace(pending.String()) != "[]" {
		t.Fatalf("pending after a send that never left: %v %s", err, pending.String())
	}
	// Without carriers configured or learned, the native failure is the answer.
	t.Setenv("AIRC_STATE_DIR", t.TempDir())
	if _, _, err := sendJSON(t, append(common, "--message", "nowhere")...); err == nil || !strings.Contains(err.Error(), "dial airc") {
		t.Fatal("send without carriers", err)
	}
}

// closedTCPPort reserves a port that is free now, below Linux's ephemeral
// range (32768+): a port from :0 is in that range, and a concurrent test's
// outgoing connection can take it before the fixture binds it again.
func closedTCPPort(t *testing.T) int {
	t.Helper()
	for range 100 {
		port := 20000 + rand.IntN(12768)
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			_ = l.Close()
			return port
		}
	}
	t.Fatal("no free port below the ephemeral range")
	return 0
}

// A connection learns the server's carriers from 005 CARRIERS; a later send
// uses them when native IRC is unreachable, with no configuration.
func TestSendUsesCarriersLearnedFromTheServer(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops"}
	native, stderr, err := sendJSON(t, append(common, "--message", "native")...)
	if err != nil || native.Carrier != "" {
		t.Fatalf("%+v %v %s", native, err, stderr)
	}
	cached := loadCarrierCache(options{addr: f.proxy.addr()})
	if protocol.FormatCarriers(cached) != "irc:"+strconv.Itoa(f.proxy.port())+","+f.carrier("http")+","+f.carrier("websocket") {
		t.Fatalf("cached %v", cached)
	}
	f.proxy.down()
	sent, stderr, err := sendJSON(t, append(common, "--message", "learned")...)
	if err != nil || sent.Carrier != f.carrier("http") {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	var doctor bytes.Buffer
	_ = run([]string{"doctor", "--addr", f.proxy.addr()}, nil, &doctor, io.Discard)
	if !strings.Contains(doctor.String(), "Fallback: reachable over "+f.carrier("http")) || !strings.Contains(doctor.String(), "Fallback carriers: "+f.carrier("http")+","+f.carrier("websocket")) {
		t.Fatalf("doctor: %s", doctor.String())
	}
	// Advertised ports describe the endpoint they came from; a list whose
	// aircd entry is another port (a tunnel) is never used.
	rememberCarriers(options{addr: "127.0.0.1:1"}, cached)
	if loadCarrierCache(options{addr: "127.0.0.1:1"}) != nil {
		t.Fatal("cached a list that does not describe its endpoint")
	}
}

// A send that landed natively, then lost its connection before the receipt,
// is retried over the gateway with the same request ID: one message.
func TestNativeSendThatLandedIsResolvedOverTheGateway(t *testing.T) {
	f := newFallbackFixture(t)
	f.proxy.setBlackhole()
	sent, stderr, err := sendJSON(t, "--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops", "--carriers", f.carrier("http"), "--message", "landed once")
	if err != nil || sent.Carrier != f.carrier("http") {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	got := historyOf(t, f.daemon, "#ops", "landed once")
	if len(got) != 1 || got[0].ID != sent.ID {
		t.Fatalf("history holds %d copies: %+v (receipt %s)", len(got), got, sent.ID)
	}
}

// A registered user falls back with its saved account token; without it the
// gateway's definite refusal is the answer, and no other carrier is tried.
func TestRegisteredUserFallsBackWithItsToken(t *testing.T) {
	f := newFallbackFixture(t)
	if err := run([]string{"user", "create", "--addr", f.proxy.addr(), "--nick", "carol"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	f.proxy.down()
	common := []string{"--addr", f.proxy.addr(), "--channel", "#ops", "--carriers", f.carrier("http") + "," + f.carrier("websocket")}
	sent, stderr, err := sendJSON(t, append(common, "--nick", "carol", "--message", "registered")...)
	if err != nil || sent.Carrier != f.carrier("http") || sent.From != "carol" {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	if got := historyOf(t, f.daemon, "#ops", "registered"); len(got) != 1 || got[0].AccountID == "" {
		t.Fatalf("registered post: %+v", got)
	}
	t.Setenv("AIRC_STATE_DIR", t.TempDir()) // no saved identity: a guest on a registered nick
	_, stderr, err = sendJSON(t, append(common, "--nick", "carol", "--message", "impostor")...)
	if err == nil || !strings.Contains(stderr, `"code":"auth_failed"`) || !strings.Contains(stderr, f.carrier("http")) {
		t.Fatalf("guest on a registered nick: %v %s", err, stderr)
	}
	if got := historyOf(t, f.daemon, "#ops", "impostor"); len(got) != 0 {
		t.Fatal("impostor posted", got)
	}
}

// The native entry for the address just tried is not tried again; other
// entries keep their order, and an invalid list is an error, not silence.
func TestFallbackEndpointsSkipThePrimary(t *testing.T) {
	agentEnv(t)
	names := func(opt options) string {
		t.Helper()
		endpoints, err := fallbackEndpoints(opt)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range endpoints {
			out = append(out, e.String())
		}
		return strings.Join(out, ",")
	}
	if got := names(options{addr: "127.0.0.1:6667", carriers: "irc:6667,irc:7000,http:6680,irc:other.example:6667"}); got != "irc:7000,http:6680,irc:other.example:6667" {
		t.Fatal(got)
	}
	unix := options{unix: "/run/airc.sock", carriers: "irc:/run/airc.sock,unix:/run/gw.sock,http:6680"}
	if got := names(unix); got != "unix:/run/gw.sock,http:6680" || primaryHost(unix) != "127.0.0.1" {
		t.Fatal(got)
	}
	if _, err := fallbackEndpoints(options{addr: "127.0.0.1:6667", carriers: "http"}); err == nil {
		t.Fatal("accepted an invalid carrier list")
	}
	if _, _, err := sendJSON(t, "--addr", "127.0.0.1:1", "--nick", "a", "--channel", "#x", "--carriers", "http", "--message", "m"); err == nil || !strings.Contains(err.Error(), "--carriers") {
		t.Fatal(err)
	}
}

// A direct message over a fallback carrier says its recipient is at work,
// as a native one does.
func TestFallbackSendSaysTheRecipientIsActive(t *testing.T) {
	f := newFallbackFixture(t)
	mustCLI(t, f.proxy.addr(), "check", "--nick", "builder", "--channel", "#ops", "--json")
	f.proxy.down()
	sent, stderr, err := sendJSON(t, "--addr", f.proxy.addr(), "--nick", "planner", "--to", "builder", "--message", "can you review?")
	if err != nil || sent.Carrier != f.carrier("http") || sent.Receipt == nil || sent.Receipt.RecipientActive == nil || !*sent.Receipt.RecipientActive || sent.Delivered == nil || *sent.Delivered {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
}
