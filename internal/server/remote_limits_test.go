package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

const (
	otherToken  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	wrongToken  = "00000000000000000000000000000000000000000000000000000000000000ff"
	failureText = "too many failed credential attempts"
)

// readUntilClosed returns every line the server sends until it closes the
// connection, failing if it does not.
func readUntilClosed(t *testing.T, conn net.Conn, reader *bufio.Reader) []string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			lines = append(lines, line)
		}
		if err == io.EOF {
			return lines
		}
		if err != nil {
			t.Fatalf("connection stayed open after %q: %v", lines, err)
		}
	}
}

func countLines(lines []string, want string) int {
	n := 0
	for _, line := range lines {
		if strings.Contains(line, want) {
			n++
		}
	}
	return n
}

func TestThirdFailedCredentialClosesConnection(t *testing.T) {
	tests := []struct {
		name    string
		server  func(t *testing.T) string
		prelude string
		attempt string
		denied  string
		noProbe bool   // the connection cannot answer PING before it authenticates
		success string // sent after the failures; it must be ignored
		granted string
	}{
		{
			name: "access token",
			server: func(t *testing.T) string {
				_, listener := accessServer(t, Config{})
				return listener.Addr().String()
			},
			noProbe: true,
			attempt: "PASS :" + wrongToken + "\r\n", denied: " 464 ",
			success: "PASS :" + strings.Repeat("a", 64) + "\r\n", granted: " 782 ",
		},
		{
			name:    "account authentication",
			server:  func(t *testing.T) string { a, _ := collaborationServer(t, t.TempDir()); return a },
			attempt: "AUTH nobody :" + wrongToken + "\r\n", denied: " 498 ",
			success: "REGISTER nobody :" + otherToken + "\r\n", granted: " 779 ",
		},
		{
			name: "registration of a taken name",
			server: func(t *testing.T) string {
				a, _ := collaborationServer(t, t.TempDir())
				createWireAccount(t, a, "taken")
				return a
			},
			attempt: "REGISTER taken :" + wrongToken + "\r\n", denied: " 498 ",
			success: "AUTH taken :" + testAdminToken + "\r\n", granted: " 779 ",
		},
		{
			name:    "SASL",
			server:  func(t *testing.T) string { a, _ := collaborationServer(t, t.TempDir()); return a },
			prelude: "CAP REQ :sasl\r\n",
			attempt: "AUTHENTICATE PLAIN\r\nAUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte("\x00nobody\x00"+wrongToken)) + "\r\n", denied: " 904 ",
			success: "PING :after\r\n", granted: "PONG server :after",
		},
		{
			name:    "operator",
			server:  func(t *testing.T) string { a, _ := adminWireServer(t); return a },
			prelude: "EPHEMERAL\r\nNICK op\r\nUSER op 0 * :op\r\n",
			attempt: "OPER :" + wrongToken + "\r\n", denied: " 464 ",
			success: "OPER :" + testAdminToken + "\r\n", granted: " 381 ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address := test.server(t)
			conn, reader := rawConn(t, address)
			if test.prelude != "" {
				io.WriteString(conn, test.prelude)
			}
			// Two failures leave the connection usable: the limit is on the third.
			io.WriteString(conn, strings.Repeat(test.attempt, 2))
			expectLine(t, reader, conn, test.denied)
			expectLine(t, reader, conn, test.denied)
			if !test.noProbe {
				io.WriteString(conn, "PING :still-open\r\n")
				expectLine(t, reader, conn, "PONG server :still-open")
			}
			io.WriteString(conn, test.attempt+test.success)
			lines := readUntilClosed(t, conn, reader)
			if countLines(lines, test.denied) != 1 || countLines(lines, failureText) != 1 {
				t.Fatalf("want the final denial and one explanation: %q", lines)
			}
			if countLines(lines, test.granted) != 0 {
				t.Fatalf("command after the limit was processed: %q", lines)
			}
		})
	}
}

func TestMixedCredentialFailuresShareOneBudget(t *testing.T) {
	address, _ := adminWireServer(t)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "EPHEMERAL\r\nNICK a\r\nUSER a 0 * :a\r\n")
	expectLine(t, reader, conn, " 001 ")
	io.WriteString(conn, "OPER :"+wrongToken+"\r\nAUTH nobody :"+wrongToken+"\r\nOPER :"+wrongToken+"\r\n")
	lines := readUntilClosed(t, conn, reader)
	if countLines(lines, " 464 ") != 2 || countLines(lines, " 498 ") != 1 || countLines(lines, failureText) != 1 {
		t.Fatalf("OPER and AUTH failures must share one budget: %q", lines)
	}
	// A new connection starts with a fresh budget.
	next, nextReader := wireRegister(t, address, "b", true)
	io.WriteString(next, "OPER :"+testAdminToken+"\r\n")
	expectLine(t, nextReader, next, " 381 ")
}

func TestRegisterCreatesOneAccountPerConnection(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "REGISTER first :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 779 ")
	io.WriteString(conn, "REGISTER second :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 437 ")
	// The limit is on creation: repeating the first registration still works,
	// and the refusal is not a failed credential.
	io.WriteString(conn, "REGISTER first :"+testAdminToken+"\r\nAUTH first :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 779 ")
	expectLine(t, reader, conn, " 779 ")
	s.mu.Lock()
	count := len(s.accounts)
	s.mu.Unlock()
	if count != 1 {
		t.Fatalf("accounts: %d", count)
	}
	createWireAccount(t, address, "second")
}

func TestAccountDeleteFreesNicknameAndRevokesOperatorRights(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	createWireAccount(t, address, "alice")
	createWireAccount(t, address, "bob")
	control, cr := wireRegister(t, address, "admin", true)
	io.WriteString(control, "ADMIN :{\"action\":\"account-list\"}\r\nOPER :"+testAdminToken+"\r\n")
	expectLine(t, cr, control, " 481 ")
	expectLine(t, cr, control, " 381 ")
	io.WriteString(control, "MODE #room +o alice\r\n")
	expectLine(t, cr, control, " MODE #room +o alice")

	io.WriteString(control, "ADMIN :{\"action\":\"account-list\"}\r\n")
	var listed []protocol.AdminResult
	for {
		line := expectLine(t, cr, control, " 77")
		command, _ := protocol.Parse(line)
		if strings.Contains(line, " 776 ") {
			break
		}
		var result protocol.AdminResult
		if err := json.Unmarshal([]byte(command.Trailing), &result); err != nil {
			t.Fatal(err)
		}
		listed = append(listed, result)
	}
	if len(listed) != 2 || listed[0].Nick != "alice" || listed[1].Nick != "bob" || !protocol.ValidMessageID(listed[0].AccountID) || listed[0].Action != "account-list" {
		t.Fatalf("list: %+v", listed)
	}

	// A live session authenticated as alice, holding an operator grant.
	op, or := rawConn(t, address)
	io.WriteString(op, "AUTH alice :"+testAdminToken+"\r\nEPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, or, op, " 001 ")
	victim, vr := wireRegister(t, address, "guest", false)
	io.WriteString(victim, "JOIN #room\r\n")
	expectLine(t, vr, victim, "JOIN #room")
	io.WriteString(op, "KICK #room nobody :probe\r\n")
	expectLine(t, or, op, " 401 ")

	// A failed write applies nothing.
	s.mu.Lock()
	original := s.accountsAt
	s.accountsAt = filepath.Join(dir, "missing", "accounts")
	s.mu.Unlock()
	io.WriteString(control, "ADMIN :{\"action\":\"account-delete\",\"nick\":\"alice\"}\r\n")
	expectLine(t, cr, control, " 437 ")
	s.mu.Lock()
	s.accountsAt = original
	_, still := s.accounts["alice"]
	s.mu.Unlock()
	if !still {
		t.Fatal("failed write removed the account")
	}

	result := adminWire(t, control, cr, protocol.AdminRequest{Action: "account-delete", Nick: "ALICE"})
	if !result.Changed || result.Nick != "alice" || result.AccountID != listed[0].AccountID {
		t.Fatalf("delete: %+v", result)
	}
	if result := adminWire(t, control, cr, protocol.AdminRequest{Action: "account-delete", Nick: "alice"}); result.Changed {
		t.Fatal("deleting an absent account reported a change")
	}

	// Nothing was disconnected, and the nickname is free for others again once
	// this session leaves; the live session just lost its account privileges.
	io.WriteString(op, "KICK #room guest :now denied\r\nPING :alive\r\n")
	expectLine(t, or, op, " 482 ")
	expectLine(t, or, op, "PONG server :alive")
	io.WriteString(control, "MODE #room\r\n")
	if line := expectLine(t, cr, control, " 324 "); strings.Contains(line, "alice") {
		t.Fatal(line)
	}
	io.WriteString(op, "QUIT\r\n")
	readUntilClosed(t, op, or)

	// The name can be claimed with a new credential and gets a new identity;
	// the old credential no longer works and the new one holds no old grant.
	other, otherReader := rawConn(t, address)
	io.WriteString(other, "AUTH alice :"+testAdminToken+"\r\n")
	expectLine(t, otherReader, other, " 498 ")
	io.WriteString(other, "REGISTER alice :"+otherToken+"\r\n")
	expectLine(t, otherReader, other, " 779 ")
	io.WriteString(other, "EPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\nKICK #room guest :no grant\r\n")
	expectLine(t, otherReader, other, " 482 ")
	s.mu.Lock()
	reissued := s.accounts["alice"].ID
	s.mu.Unlock()
	if reissued == listed[0].AccountID {
		t.Fatal("account ID was reused")
	}

	// Deletions are durable.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	address, _ = collaborationServer(t, dir)
	again, ar := wireRegister(t, address, "again", true)
	io.WriteString(again, "OPER :"+testAdminToken+"\r\nADMIN :{\"action\":\"account-delete\",\"nick\":\"bob\"}\r\n")
	expectLine(t, ar, again, " 381 ")
	expectLine(t, ar, again, "\"changed\":true")
	io.WriteString(again, "ADMIN :{\"action\":\"account-list\"}\r\n")
	if line := expectLine(t, ar, again, " 775 "); !strings.Contains(line, "alice") || !strings.Contains(line, reissued) {
		t.Fatal(line)
	}
	if line := expectLine(t, ar, again, " 776 "); line == "" {
		t.Fatal("list not terminated")
	}
}

func TestAccountAdminRequestValidation(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	control, cr := wireRegister(t, address, "admin", true)
	io.WriteString(control, "OPER :"+testAdminToken+"\r\n")
	expectLine(t, cr, control, " 381 ")
	for _, body := range []string{
		`{"action":"account-delete"}`,
		`{"action":"account-delete","nick":"bad nick"}`,
		`{"action":"account-delete","nick":"alice","scope":"#room"}`,
		`{"action":"account-delete","nick":"alice","reason":"x"}`,
		`{"action":"account-delete","nick":"alice","seconds":5}`,
		`{"action":"account-list","nick":"alice"}`,
		`{"action":"account-list","scope":"#room"}`,
	} {
		io.WriteString(control, "ADMIN :"+body+"\r\n")
		expectLine(t, cr, control, " 461 ")
	}
	plain, _ := adminWireServer(t)
	conn, reader := wireRegister(t, plain, "admin", true)
	io.WriteString(conn, "OPER :"+testAdminToken+"\r\nADMIN :{\"action\":\"account-list\"}\r\nADMIN :{\"action\":\"account-delete\",\"nick\":\"x\"}\r\n")
	expectLine(t, reader, conn, " 381 ")
	expectLine(t, reader, conn, " 437 ")
	expectLine(t, reader, conn, " 437 ")
}

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
	for max, want := range map[int]int{4: 4, 8: 4, 16: 4, 100: 25, 512: 128} {
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

func TestAccountCreationIsLimitedPerRemoteAddressOverTime(t *testing.T) {
	now := time.Now()
	s := New(Config{Now: func() time.Time { return now }})
	s.mu.Lock()
	defer s.mu.Unlock()
	for range maxRegistrationsPerAddress {
		if !s.registrationAllowedLocked("203.0.113.7") {
			t.Fatal("refused before the limit")
		}
		s.noteRegistrationLocked("203.0.113.7")
	}
	if s.registrationAllowedLocked("203.0.113.7") {
		t.Fatal("an address exceeded its account creation limit by reconnecting")
	}
	if !s.registrationAllowedLocked("203.0.113.8") || !s.registrationAllowedLocked("") {
		t.Fatal("another address, or a local peer, was limited")
	}
	now = now.Add(registrationWindow + time.Second)
	if !s.registrationAllowedLocked("203.0.113.7") || len(s.registrations) != 0 {
		t.Fatalf("the limit did not lapse with the window: %v", s.registrations)
	}
}
