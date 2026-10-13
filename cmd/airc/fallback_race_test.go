//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
)

// silentNative takes over a native port with a listener that accepts and
// never answers, as when aircd hangs or a middlebox swallows the traffic. It
// counts the connections it accepted.
func silentNative(t *testing.T, address string) *atomic.Int32 {
	t.Helper()
	var l net.Listener
	var err error
	for range 50 { // the proxy's port is free again once it is down
		if l, err = net.Listen("tcp", address); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
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
