package fallback

import (
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testwait"
)

// delayUDP relays datagrams between one client and address, holding each
// one from address for delay before passing it on, so a dial sees the
// gateway answer slowly.
func delayUDP(t *testing.T, address string, delay time.Duration) int {
	t.Helper()
	target, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	listen, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	up, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listen.Close()
		_ = up.Close()
	})
	var mu sync.Mutex
	var client *net.UDPAddr
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := listen.ReadFromUDP(buf)
			if err != nil {
				return
			}
			mu.Lock()
			client = from
			mu.Unlock()
			_, _ = up.WriteToUDP(buf[:n], target)
		}
	}()
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := up.Read(buf)
			if err != nil {
				return
			}
			p := append([]byte(nil), buf[:n]...)
			time.AfterFunc(delay, func() {
				mu.Lock()
				to := client
				mu.Unlock()
				if to != nil {
					_, _ = listen.WriteToUDP(p, to)
				}
			})
		}
	}()
	return listen.LocalAddr().(*net.UDPAddr).Port
}

// A resumed session's dial returns before the handshake (its 0-RTT keys are
// enough to send), but the HTTP/3 client is built only once the handshake
// has completed: a stream opened earlier races, inside quic-go, with the
// handshake installing the gateway's transport parameters
// (quic-go/quic-go#4303). A gateway answering slowly keeps that window open
// for the whole dial, on the connection raced to and on a redial.
func TestHTTP3ClientWaitsForTheHandshake(t *testing.T) {
	g := newFakeGateway(t)
	port, trust := serveHTTP3(t, g)
	slow := delayUDP(t, "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
	e, _ := ParseEndpoint("http3:127.0.0.1:" + strconv.Itoa(slow))
	c := &Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: trust, Endpoints: []Endpoint{e}}
	read := func(k *Conn, id string) {
		t.Helper()
		if _, err := k.gateway.call(t.Context(), request{RequestID: id, Operation: "history.read", Target: targetOf("#room"), Limit: 1, Start: "recent"}); err != nil {
			t.Fatal(err)
		}
	}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	read(k, "a")
	testwait.Every(t, 2*time.Second, 10*time.Millisecond, "the session ticket to be cached", func() bool {
		_, ok := quicSessions.Get("127.0.0.1")
		return ok
	})
	_ = k.Close()
	handshook := func(h *http3Conn) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		select {
		case <-h.q.conn.HandshakeComplete():
			return true
		default:
			return false
		}
	}
	k, err = c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	h := k.gateway.(*http3Conn)
	early := !handshook(h)
	read(k, "b") // the request waits for the handshake either way
	if early {
		t.Fatal("the HTTP/3 client was built before the handshake completed")
	}
	if !h.Resumed0RTT() {
		t.Fatal("the dial did not resume the session")
	}
	first := h.q.conn
	_ = first.CloseWithError(0, "idle") // as the gateway does after thirty idle seconds
	<-first.Context().Done()
	read(k, "c")
	if h.q.conn == first || !handshook(h) || !h.Resumed0RTT() {
		t.Fatalf("after the redial: same connection %v, handshake complete %v, resumed %v", h.q.conn == first, handshook(h), h.Resumed0RTT())
	}
}
