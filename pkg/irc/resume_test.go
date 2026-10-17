package irc_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// silencingProxy relays TCP connections to upstream until Silence is called;
// from then on the relayed bytes of existing connections are dropped in both
// directions while both sockets stay open, like a link that died without
// either end noticing. New connections are relayed normally.
type silencingProxy struct {
	listener net.Listener
	mu       sync.Mutex
	cut      int // connections accepted before this many cuts are dead
}

func newSilencingProxy(t *testing.T, upstream string) *silencingProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &silencingProxy{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = client.Close()
				continue
			}
			p.mu.Lock()
			born := p.cut
			p.mu.Unlock()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			relay := func(dst, src net.Conn) {
				dead := func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.cut > born }
				buffer := make([]byte, 4096)
				for {
					n, err := src.Read(buffer)
					if err != nil {
						if !dead() {
							_ = dst.Close()
						}
						return // a dead link's other end never learns of the close
					}
					if dead() {
						continue // nothing gets through
					}
					if _, err := dst.Write(buffer[:n]); err != nil {
						return
					}
				}
			}
			go relay(server, client)
			go relay(client, server)
		}
	}()
	return p
}

// Silence kills every connection relayed so far, silently.
func (p *silencingProxy) Silence() { p.mu.Lock(); p.cut++; p.mu.Unlock() }

func (p *silencingProxy) Addr() string { return p.listener.Addr().String() }

// A persistent client whose link went silent redials before the server has
// noticed, while the server still holds its session and nickname. With the
// resume secret the new connection takes that session over at once instead
// of being refused the nickname until the server's read timeout.
func TestReconnectTakesOverTheSessionTheServerStillHolds(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 32}, nil)
	proxy := newSilencingProxy(t, address)
	client, err := irc.Dial(irc.Config{Nick: "bot", Addr: proxy.Addr(), Reconnect: true, KeepAlive: 100 * time.Millisecond, ProbeTimeout: 100 * time.Millisecond, MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if !client.Resumable() {
		t.Fatal("the server issued no resume secret")
	}
	if err := client.Join("#room"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.JoinEvent)
		return ok && value.Agent == "bot" && value.Channel == "#room"
	})
	peer := newClient(t, "peer", address)
	if err := peer.Join("#room"); err != nil {
		t.Fatal(err)
	}
	joinAndWait(t, peer, "#room")

	proxy.Silence()
	start := time.Now()
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.ConnectionEvent)
		return ok && !value.Connected
	})
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.ConnectionEvent)
		return ok && value.Connected
	})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("reconnected after %s: the nickname was not reclaimed promptly", elapsed)
	}
	// The room never saw it leave, and it is a member again without a new JOIN.
	if err := peer.Send("#room", "still there?"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.MessageEvent)
		return ok && value.Message == "still there?"
	})
	for {
		select {
		case event := <-peer.Events():
			switch value := event.(type) {
			case *irc.QuitEvent:
				t.Fatalf("the room saw the stale session quit: %+v", value)
			case *irc.JoinEvent:
				if value.Agent == "bot" {
					t.Fatalf("the room saw the client join again: %+v", value)
				}
			}
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}
