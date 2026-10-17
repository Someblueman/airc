package server

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.remote }

func pendingFixture(t *testing.T, cfg Config) *Server {
	t.Helper()
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.PingInterval, cfg.ReadTimeout, cfg.RegistrationTimeout = time.Hour, time.Hour, time.Hour
	s := New(cfg)
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s
}

// connect offers the server a pipe that claims to come from remote and reports
// whether the server kept it. A kept connection is left for the caller to read;
// a refused one has been closed by the server.
func connect(t *testing.T, s *Server, remote net.Addr) (peer net.Conn, kept bool) {
	t.Helper()
	server, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	s.mu.Lock()
	before := len(s.clients)
	s.mu.Unlock()
	s.accept(addrConn{server, remote})
	s.mu.Lock()
	defer s.mu.Unlock()
	return peer, len(s.clients) > before
}

func tcpAddr(ip string) net.Addr { return &net.TCPAddr{IP: net.ParseIP(ip), Port: 40000} }

func (s *Server) pendingCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[key]
}

func TestUnregisteredConnectionsPerRemoteAddressAreCapped(t *testing.T) {
	s := pendingFixture(t, Config{MaxConnections: 32})
	if s.cfg.MaxPendingPerAddress != 8 {
		t.Fatalf("default cap: %d", s.cfg.MaxPendingPerAddress)
	}
	s.cfg.MaxPendingPerAddress = 3
	var peers []net.Conn
	for i := range 3 {
		peer, kept := connect(t, s, tcpAddr("203.0.113.9"))
		if !kept {
			t.Fatalf("connection %d refused", i)
		}
		peers = append(peers, peer)
	}

	// The refusal is the one a full server sends.
	server, peer := net.Pipe()
	defer peer.Close()
	refused := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(peer).ReadString('\n')
		refused <- line
	}()
	s.accept(addrConn{server, tcpAddr("203.0.113.9")})
	if line := <-refused; !strings.Contains(line, "ERROR :server is full or shutting down") {
		t.Fatalf("refusal: %q", line)
	}

	other, kept := connect(t, s, tcpAddr("203.0.113.10"))
	if !kept {
		t.Fatal("another address was refused")
	}
	// IPv6 peers share a quota across their /64.
	for _, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2::a", "2001:db8:1:2:1::b"} {
		if _, kept := connect(t, s, tcpAddr(ip)); !kept {
			t.Fatalf("IPv6 peer %s refused early", ip)
		}
	}
	if _, kept := connect(t, s, tcpAddr("2001:db8:1:2:ffff::1")); kept {
		t.Fatal("same /64 was not limited")
	}
	if _, kept := connect(t, s, tcpAddr("2001:db8:1:3::1")); !kept {
		t.Fatal("a different /64 was limited")
	}
	if n := s.pendingCount("203.0.113.9"); n != 3 {
		t.Fatalf("pending: %d", n)
	}

	// Registering frees a slot.
	io.WriteString(peers[0], "NICK one\r\nUSER one 0 * :one\r\n")
	expectLine(t, bufio.NewReader(peers[0]), peers[0], " 001 ")
	if n := s.pendingCount("203.0.113.9"); n != 2 {
		t.Fatalf("pending after registration: %d", n)
	}
	if _, kept := connect(t, s, tcpAddr("203.0.113.9")); !kept {
		t.Fatal("slot freed by registration was not reusable")
	}

	// So does disconnecting; the entry disappears rather than accumulating.
	s.mu.Lock()
	var gone *session
	for _, c := range s.clients {
		if c.pendingKey == "203.0.113.10" {
			gone = c
		}
	}
	s.mu.Unlock()
	other.Close()
	<-gone.done // remove closes the session and releases its slot under one lock
	if n := s.pendingCount("203.0.113.10"); n != 0 {
		t.Fatalf("pending after disconnect: %d", n)
	}
	s.mu.Lock()
	_, retained := s.pending["203.0.113.10"]
	s.mu.Unlock()
	if retained {
		t.Fatal("empty pending entry retained")
	}
}

func TestLoopbackAndUnixPeersAreNotAddressLimited(t *testing.T) {
	for _, remote := range []net.Addr{tcpAddr("127.0.0.1"), tcpAddr("::1"), &net.UnixAddr{Name: "@", Net: "unix"}} {
		s := pendingFixture(t, Config{MaxConnections: 16})
		for i := range 6 {
			if _, kept := connect(t, s, remote); !kept {
				t.Fatalf("%v connection %d refused", remote, i)
			}
		}
		if len(s.pending) != 0 {
			t.Fatalf("exempt peer %v was counted: %v", remote, s.pending)
		}
	}
}

func TestPendingAddressCapDefaults(t *testing.T) {
	for max, want := range map[int]int{4: 4, 8: 4, 16: 4, 100: 25, 512: 128, 4096: 128} {
		if got := New(Config{MaxConnections: max}).cfg.MaxPendingPerAddress; got != want {
			t.Errorf("MaxConnections %d: cap %d, want %d", max, got, want)
		}
	}
	if got := New(Config{}).cfg.MaxPendingPerAddress; got != 128 {
		t.Errorf("default cap %d", got)
	}
	if got := New(Config{MaxPendingPerAddress: 9}).cfg.MaxPendingPerAddress; got != 9 {
		t.Errorf("explicit cap %d", got)
	}
}
