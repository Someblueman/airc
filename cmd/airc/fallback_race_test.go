//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/fallback"
)

// listenFreedPort listens on address once the process that held it has let
// it go, within a second.
func listenFreedPort(t *testing.T, address string) net.Listener {
	t.Helper()
	var l net.Listener
	testwait.Every(t, time.Second, 20*time.Millisecond, "the native port to be free", func() bool {
		var err error
		l, err = net.Listen("tcp", address)
		return err == nil
	})
	return l
}

// silentNative takes over a native port with a listener that accepts and
// never answers, as when aircd hangs or a middlebox swallows the traffic. It
// counts the connections it accepted.
func silentNative(t *testing.T, address string) *atomic.Int32 {
	t.Helper()
	l := listenFreedPort(t, address) // the proxy's port is free again once it is down
	var accepted atomic.Int32
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return &accepted
}

// A native port that accepts and never answers costs a send the race's
// stagger, not the native connect budget. The carrier that answered is then
// tried first, until its memory expires.
func TestSendRacesPastASilentNativePortAndRemembersTheCarrier(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops"}
	if _, stderr, err := sendJSON(t, append(common, "--message", "native")...); err != nil {
		t.Fatal(err, stderr) // learns the carriers
	}
	f.proxy.down()
	accepted := silentNative(t, f.proxy.addr())
	started := time.Now()
	sent, stderr, err := sendJSON(t, append(common, "--message", "raced")...)
	if err != nil || sent.Carrier != f.carrier("http") {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	if elapsed := time.Since(started); elapsed >= nativeDialBudget/2 {
		t.Fatalf("send took %s past a silent native port", elapsed)
	}
	if accepted.Load() != 1 {
		t.Fatalf("native port accepted %d connections", accepted.Load())
	}
	opt := options{addr: f.proxy.addr()}
	if last := loadLastCarrier(opt); last != f.carrier("http") {
		t.Fatalf("remembered %q", last)
	}
	// Remembered: the carrier goes first and answers before native IRC is
	// even dialled.
	var stdout bytes.Buffer
	if err := run([]string{"history", "#ops", "--addr", f.proxy.addr(), "--json"}, nil, &stdout, io.Discard); err != nil || !strings.Contains(stdout.String(), `"raced"`) {
		t.Fatalf("history: %v %s", err, stdout.String())
	}
	if accepted.Load() != 1 {
		t.Fatalf("native IRC was dialled before the remembered carrier (%d connections)", accepted.Load())
	}
	// A memory past its TTL is ignored; a damaged one never breaks a command.
	path, err := lastCarrierPath(opt)
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := json.Marshal(lastCarrier{Server: serverKey(opt), Carrier: f.carrier("http"), At: time.Now().Add(-lastCarrierTTL - time.Minute)})
	for _, data := range [][]byte{stale, []byte("{not json")} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if loadLastCarrier(opt) != "" {
			t.Fatalf("used %s", data)
		}
		if sent, stderr, err := sendJSON(t, append(common, "--message", "again")...); err != nil || sent.Carrier != f.carrier("http") {
			t.Fatalf("%+v %v %s", sent, err, stderr)
		}
	}
	if accepted.Load() != 3 {
		t.Fatalf("native IRC was not tried first without a memory (%d connections)", accepted.Load())
	}
}

// Native IRC answering again forgets the remembered carrier.
func TestNativeAnswerForgetsTheRememberedCarrier(t *testing.T) {
	f := newFallbackFixture(t)
	opt := options{addr: f.proxy.addr()}
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops"}
	if _, stderr, err := sendJSON(t, append(common, "--message", "learn")...); err != nil {
		t.Fatal(err, stderr)
	}
	// A remembered carrier that is down costs nothing: native IRC follows
	// at once, answers, and the memory is dropped.
	down := "http:" + strconv.Itoa(closedTCPPort(t))
	e, err := fallback.ParseEndpoint(down)
	if err != nil {
		t.Fatal(err)
	}
	answeredBy(opt, reached{}, e)
	if loadLastCarrier(opt) != down {
		t.Fatal("nothing remembered")
	}
	sent, stderr, err := sendJSON(t, append(common, "--carriers", down+","+f.carrier("http"), "--message", "native again")...)
	if err != nil || sent.Carrier != "" {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	if last := loadLastCarrier(opt); last != "" {
		t.Fatalf("still remembers %q", last)
	}
}

// With native IRC down, a reply or reaction whose conversation is named goes
// over a gateway carrier; without the conversation it still needs native IRC.
func TestRepliesAndReactionsFallBackWithTheirConversation(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice"}
	root, stderr, err := sendJSON(t, append(common, "--channel", "#ops", "--message", "root")...)
	if err != nil {
		t.Fatal(err, stderr)
	}
	f.proxy.down()
	reply, stderr, err := sendJSON(t, append(common, "--reply-to", root.ID, "--channel", "#ops", "--message", "a reply")...)
	if err != nil || reply.Carrier != f.carrier("http") || reply.ReplyTo != root.ID || reply.Target != "#ops" || !protocol.ValidMessageID(reply.ID) {
		t.Fatalf("reply: %+v %v %s", reply, err, stderr)
	}
	again, _, err := sendJSON(t, append(common, "--reply-to", root.ID, "--channel", "#ops", "--message", "a reply", "--request-id", reply.RequestID)...)
	if err != nil || again.ID != reply.ID {
		t.Fatal("a retried reply posted again", again, err)
	}
	var out bytes.Buffer
	if err := run(append([]string{"react", root.ID, "agree", "--channel", "#ops"}, common...), nil, &out, io.Discard); err != nil || !strings.Contains(out.String(), "(reaction to "+root.ID+")") {
		t.Fatalf("reaction: %v %q", err, out.String())
	}
	got := historyOf(t, f.daemon, "#ops", "a reply")
	if len(got) != 1 || got[0].ReplyTo != root.ID || got[0].From != "alice" {
		t.Fatalf("reply in history: %+v", got)
	}
	if reactions := historyOf(t, f.daemon, "#ops", "agree"); len(reactions) != 1 || reactions[0].Reaction != "agree" || reactions[0].ReplyTo != root.ID {
		t.Fatalf("reaction in history: %+v", reactions)
	}
	// The gateway checks the named conversation: a wrong room is refused.
	if _, stderr, err := sendJSON(t, append(common, "--reply-to", root.ID, "--channel", "#elsewhere", "--message", "lost")...); err == nil || !strings.Contains(stderr, `"code"`) {
		t.Fatalf("wrong room: %v %s", err, stderr)
	}
	if got := historyOf(t, f.daemon, "#elsewhere", "lost"); len(got) != 0 {
		t.Fatal("posted to the wrong room", got)
	}
	if _, _, err := sendJSON(t, append(common, "--reply-to", root.ID, "--message", "no conversation")...); err == nil || !strings.Contains(err.Error(), "dial airc") {
		t.Fatalf("reply without its conversation: %v", err)
	}
}

var lastSeen = regexp.MustCompile(`"last_seen":"[^"]*"|last seen [0-9TZ:.+-]+`)

// The reads that fall back (a room's latest history, the first page of a
// room's search matches, the room list and agent cards) print the same over a
// gateway carrier as over native IRC.
func TestReadsPrintTheSameOverAnyCarrier(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice"}
	for _, body := range []string{"needle one", "hay", "needle two"} {
		if _, stderr, err := sendJSON(t, append(common, "--channel", "#ops", "--message", body)...); err != nil {
			t.Fatal(err, stderr)
		}
	}
	if err := run(append([]string{"profile", "--model", "Claude", "--about", "fallback tester"}, common...), nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"history", "#ops", "--limit", "2"},
		{"history", "#ops", "--json"},
		{"search", "needle", "--target", "#ops"},
		{"search", "NEEDLE", "--target", "#ops", "--from", "alice", "--limit", "1", "--json"},
		{"channels"},
		{"channels", "--json"},
		{"directory"},
		{"directory", "--who", "ALICE", "--json"},
		{"profile"},
	}
	read := func() []string {
		var out []string
		for _, args := range commands {
			var stdout, stderr bytes.Buffer
			if err := run(append(slices.Clone(args), common...), nil, &stdout, &stderr); err != nil {
				t.Fatalf("%v: %v %s", args, err, stderr.String())
			}
			// Each command's own connection as alice moves her last-seen time.
			out = append(out, lastSeen.ReplaceAllString(stdout.String()+stderr.String(), "last seen"))
		}
		return out
	}
	native := read()
	f.proxy.down()
	fallback := read()
	if loadLastCarrier(options{addr: f.proxy.addr()}) != f.carrier("http") {
		t.Fatal("history and search were not read over the gateway")
	}
	for i := range commands {
		if native[i] != fallback[i] || native[i] == "" {
			t.Errorf("%v\nnative:\n%s\ngateway:\n%s", commands[i], native[i], fallback[i])
		}
	}
}

// slowNative takes over a native port with a forwarder to the daemon that
// holds each connection silent for delay first: slower than the race's
// stagger, well within the native connect budget.
func slowNative(t *testing.T, address, daemon string, delay time.Duration) {
	t.Helper()
	l := listenFreedPort(t, address)
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			client, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				time.Sleep(delay)
				server, err := net.Dial("tcp", daemon)
				if err != nil {
					_ = client.Close()
					return
				}
				mu.Lock()
				conns = append(conns, client, server)
				mu.Unlock()
				go func() { _, _ = io.Copy(server, client); _ = server.Close() }()
				_, _ = io.Copy(client, server)
				_ = client.Close()
			}()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
}

// A native server slower than the stagger loses the race to another carrier,
// but send --check, which reads over native IRC, waits for native IRC
// instead of racing it, remembered carrier or not.
func TestSendCheckWaitsForASlowNativeServer(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops"}
	if _, stderr, err := sendJSON(t, append(common, "--message", "learn")...); err != nil {
		t.Fatal(err, stderr)
	}
	f.proxy.close()
	slowNative(t, f.proxy.addr(), f.daemon, time.Second)
	sent, stderr, err := sendJSON(t, append(common, "--message", "raced")...)
	if err != nil || sent.Carrier != f.carrier("http") {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	// --check prints the receipt, then the check's own lines.
	var stdout, errOut bytes.Buffer
	err = run(append([]string{"send", "--json", "--check", "--message", "checked"}, common...), strings.NewReader(""), &stdout, &errOut)
	var checked sendResult
	if err != nil || json.NewDecoder(&stdout).Decode(&checked) != nil || checked.Carrier != "" || checked.Message != "checked" {
		t.Fatalf("--check: %+v %v %s", checked, err, errOut.String())
	}
}

// A long-lived session whose connection died races for a new one instead of
// failing on native IRC alone.
func TestDeadSessionConnectionFallsBack(t *testing.T) {
	f := newFallbackFixture(t)
	session := &agentConnection{}
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice"}
	if _, stderr, err := sendJSON(t, append(common, "--channel", "#ops", "--message", "kept")...); err != nil {
		t.Fatal(err, stderr) // learns the carriers
	}
	var stdout bytes.Buffer
	if err := runHistorySession(t.Context(), session, append([]string{"#ops", "--json"}, common...), &stdout, io.Discard); err != nil || session.client == nil {
		t.Fatalf("native history: %v %v", err, session.client)
	}
	f.proxy.close()
	select {
	case <-session.client.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session connection did not notice the close")
	}
	stdout.Reset()
	if err := runHistorySession(t.Context(), session, append([]string{"#ops", "--json"}, common...), &stdout, io.Discard); err != nil || !strings.Contains(stdout.String(), `"kept"`) {
		t.Fatalf("history after the connection died: %v %s", err, stdout.String())
	}
	if session.client != nil {
		t.Fatal("kept a dead connection")
	}
}

// A native server that goes silent once a send is written is detected by a
// probe in about a second, and the send is resolved over another carrier with
// the same request ID, even while the native port still accepts connections
// and then says nothing.
func TestSendThatGoesSilentIsResolvedInAboutASecond(t *testing.T) {
	f := newFallbackFixture(t)
	common := []string{"--addr", f.proxy.addr(), "--nick", "alice", "--channel", "#ops"}
	if _, stderr, err := sendJSON(t, append(common, "--message", "learn")...); err != nil {
		t.Fatal(err, stderr) // learns the carriers
	}
	f.proxy.setBlackhole()
	taken := make(chan net.Listener, 1)
	go func() {
		// The proxy stops accepting when the send goes silent; a listener
		// that accepts and never answers then takes the port over.
		defer close(taken)
		if !testwait.Poll(2*time.Second, 10*time.Millisecond, f.proxy.isDown) {
			return // the test reports the port never taken
		}
		var l net.Listener
		if !testwait.Poll(time.Second, 10*time.Millisecond, func() bool {
			var err error
			l, err = net.Listen("tcp", f.proxy.addr())
			return err == nil
		}) {
			return
		}
		taken <- l
		var conns []net.Conn
		for {
			conn, err := l.Accept()
			if err != nil {
				for _, c := range conns {
					_ = c.Close()
				}
				return
			}
			conns = append(conns, conn)
		}
	}()
	t.Cleanup(func() {
		if l, ok := <-taken; ok {
			_ = l.Close()
		}
	})
	started := time.Now()
	sent, stderr, err := sendJSON(t, append(common, "--message", "landed once")...)
	if err != nil || sent.Carrier != f.carrier("http") {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
	if elapsed := time.Since(started); elapsed > 2500*time.Millisecond {
		t.Fatalf("resolved after %s", elapsed)
	}
	if got := historyOf(t, f.daemon, "#ops", "landed once"); len(got) != 1 || got[0].ID != sent.ID {
		t.Fatalf("history holds %d copies: %+v (receipt %s)", len(got), got, sent.ID)
	}
	select {
	case l, ok := <-taken:
		if !ok {
			t.Fatal("the silent listener never took the native port")
		}
		taken <- l
	case <-time.After(time.Second):
		t.Fatal("the silent listener never took the native port")
	}
}
