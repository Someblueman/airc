package server

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testGatewayToken = "5555555555555555555555555555555555555555555555555555555555555555"

// testClock is a Config.Now that tests advance explicitly.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// gatewayServer starts a loopback server with accounts, an admin credential
// and the gateway credential testGatewayToken.
func gatewayServer(t *testing.T, cfg Config) (string, *Server) {
	t.Helper()
	cfg.PingInterval, cfg.ReadTimeout = time.Hour, time.Hour
	dir := t.TempDir()
	return startServer(t, cfg, func(s *Server) error {
		for _, err := range []error{s.EnableAdmin(testAdminToken), s.RestoreAccounts(filepath.Join(dir, "accounts")), s.EnableGateway(testGatewayToken)} {
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// asserted opens a loopback connection that asserts source with WEBIRC.
func asserted(t *testing.T, address, source string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "WEBIRC "+source+" :"+testGatewayToken+"\r\n")
	expectLine(t, reader, conn, " 786 ")
	return conn, reader
}

// expectAny reads lines until one contains any of wants and returns it.
func expectAny(t *testing.T, reader *bufio.Reader, conn net.Conn, wants ...string) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for %q: %v", wants, err)
		}
		for _, want := range wants {
			if strings.Contains(line, want) {
				return line
			}
		}
	}
}

// registerFrom creates an account from a connection asserting source, or
// from a plain loopback connection when source is empty, and returns the reply.
func registerFrom(t *testing.T, address, source, nick string) string {
	t.Helper()
	var conn net.Conn
	var reader *bufio.Reader
	if source == "" {
		conn, reader = rawConn(t, address)
	} else {
		conn, reader = asserted(t, address, source)
	}
	defer conn.Close()
	io.WriteString(conn, "REGISTER "+nick+" :"+otherToken+"\r\n")
	return expectAny(t, reader, conn, " 779 ", " 437 ")
}

func TestWebircAppliesRegistrationLimitToAssertedSource(t *testing.T) {
	for _, test := range []struct{ name, source, same, other string }{
		{"IPv4", "203.0.113.9", "203.0.113.9", "203.0.113.10"},
		{"IPv6 by /64", "2001:db8:1:2::1", "2001:db8:1:2:ffff::9", "2001:db8:1:3::1"},
		{"label", "listener:mqtt", "listener:mqtt", "listener:amqp"},
		{"asserted loopback", "127.0.0.1", "127.0.0.1", "route:redis-requests"},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, _ := gatewayServer(t, Config{})
			for i := range maxRegistrationsPerAddress {
				if line := registerFrom(t, address, test.source, fmt.Sprintf("user%d", i)); !strings.Contains(line, " 779 ") {
					t.Fatalf("registration %d refused: %q", i, line)
				}
			}
			if line := registerFrom(t, address, test.same, "over"); !strings.Contains(line, " 437 ") || !strings.Contains(line, "this address") {
				t.Fatalf("source exceeded its limit by reconnecting through the gateway: %q", line)
			}
			if line := registerFrom(t, address, test.other, "other"); !strings.Contains(line, " 779 ") {
				t.Fatalf("another source was limited: %q", line)
			}
			if line := registerFrom(t, address, "", "local"); !strings.Contains(line, " 779 ") {
				t.Fatalf("a direct loopback client was limited: %q", line)
			}
		})
	}
}

func TestWebircRequiresGatewayCredentialAndComesFirst(t *testing.T) {
	address, _ := gatewayServer(t, Config{})
	for _, test := range []struct{ name, send, denied string }{
		{"wrong credential", "WEBIRC 203.0.113.9 :" + wrongToken + "\r\n", " 464 "},
		{"missing credential", "WEBIRC 203.0.113.9\r\n", " 464 "},
		{"after another command", "NICK a\r\nWEBIRC 203.0.113.9 :" + testGatewayToken + "\r\n", " 462 "},
		{"after PASS", "PASS :" + wrongToken + "\r\nWEBIRC 203.0.113.9 :" + testGatewayToken + "\r\n", " 462 "},
		{"invalid source", "WEBIRC 203.0.113.9:4000 :" + testGatewayToken + "\r\n", " 461 "},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, reader := rawConn(t, address)
			// Commands pipelined behind a refused assertion never run.
			io.WriteString(conn, test.send+"REGISTER piped :"+otherToken+"\r\nNICK piped\r\nUSER u 0 * :u\r\n")
			lines := readUntilClosed(t, conn, reader)
			if countLines(lines, test.denied) != 1 || countLines(lines, "ERROR :source not accepted") != 1 || countLines(lines, " 786 ") != 0 || countLines(lines, " 779 ") != 0 || countLines(lines, " 001 ") != 0 {
				t.Fatalf("assertion not refused: %q", lines)
			}
		})
	}

	// A configured server advertises the command; normal clients are unaffected.
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "NICK plain2\r\nUSER u 0 * :u\r\n")
	if line := expectLine(t, reader, conn, " 005 "); !strings.Contains(line, " WEBIRC=1") {
		t.Fatalf("WEBIRC not advertised: %q", line)
	}

	// Without a gateway credential the command does not exist.
	plain := rawServer(t)
	conn, reader = rawConn(t, plain)
	io.WriteString(conn, "WEBIRC 203.0.113.9 :"+testGatewayToken+"\r\nNICK a\r\nUSER u 0 * :u\r\n")
	expectLine(t, reader, conn, " 451 ")
	if line := expectLine(t, reader, conn, " 005 "); strings.Contains(line, "WEBIRC") {
		t.Fatalf("WEBIRC advertised without a gateway credential: %q", line)
	}
	expectLine(t, reader, conn, " 001 ")
}

func TestWebircFailedCredentialsAreThrottledPerAssertedSource(t *testing.T) {
	clock := &testClock{now: time.Now()}
	address, _ := gatewayServer(t, Config{Now: clock.Now})
	createWireAccount(t, address, "alice")
	good := "AUTH alice :" + testAdminToken + "\r\n"

	// The gateway opens a fresh connection per attempt; each one is closed
	// at the per-connection limit, but the source keeps the count.
	for range maxFailuresPerAddress / maxCredentialFailures {
		conn, reader := asserted(t, address, "198.51.100.7")
		io.WriteString(conn, strings.Repeat("AUTH alice :"+wrongToken+"\r\n", maxCredentialFailures))
		if lines := readUntilClosed(t, conn, reader); countLines(lines, failureText) != 1 {
			t.Fatalf("per-connection limit: %q", lines)
		}
	}
	conn, reader := asserted(t, address, "198.51.100.7")
	io.WriteString(conn, good)
	if line := expectAny(t, reader, conn, " 498 ", " 779 "); !strings.Contains(line, " 498 ") || !strings.Contains(line, "from this address") {
		t.Fatalf("correct credential checked while the source is throttled: %q", line)
	}
	// Operator and SASL credentials share the throttle.
	io.WriteString(conn, "EPHEMERAL\r\nNICK op\r\nUSER u 0 * :u\r\nOPER :"+testAdminToken+"\r\n")
	if line := expectAny(t, reader, conn, " 464 ", " 381 "); !strings.Contains(line, "from this address") {
		t.Fatalf("operator credential checked while throttled: %q", line)
	}
	conn, reader = asserted(t, address, "198.51.100.7")
	io.WriteString(conn, "CAP REQ :sasl\r\nAUTHENTICATE PLAIN\r\nAUTHENTICATE "+base64.StdEncoding.EncodeToString([]byte("\x00alice\x00"+testAdminToken))+"\r\n")
	if line := expectAny(t, reader, conn, " 904 ", " 903 "); !strings.Contains(line, "from this address") {
		t.Fatalf("SASL credential checked while throttled: %q", line)
	}

	for _, other := range []string{"198.51.100.8", "listener:mqtt"} {
		conn, reader := asserted(t, address, other)
		io.WriteString(conn, good)
		expectLine(t, reader, conn, " 779 ")
	}
	direct, directReader := rawConn(t, address)
	io.WriteString(direct, good)
	expectLine(t, directReader, direct, " 779 ")

	clock.Advance(failureWindow)
	conn, reader = asserted(t, address, "198.51.100.7")
	io.WriteString(conn, good)
	expectLine(t, reader, conn, " 779 ")
}

func TestWebircUnregisteredCapUsesAssertedSource(t *testing.T) {
	address, _ := gatewayServer(t, Config{MaxPendingPerAddress: 2})
	first, firstReader := asserted(t, address, "203.0.113.9")
	asserted(t, address, "203.0.113.9")
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "WEBIRC 203.0.113.9 :"+testGatewayToken+"\r\n")
	if lines := readUntilClosed(t, conn, reader); countLines(lines, "ERROR :server is full or shutting down") != 1 || countLines(lines, " 786 ") != 0 {
		t.Fatalf("third unregistered connection from the source: %q", lines)
	}
	asserted(t, address, "203.0.113.10")
	// The gateway's own loopback connections are not counted.
	for range 3 {
		rawConn(t, address)
	}
	io.WriteString(first, "EPHEMERAL\r\nNICK one\r\nUSER u 0 * :u\r\n")
	expectLine(t, firstReader, first, " 001 ")
	asserted(t, address, "203.0.113.9")
}

func TestGatewayCredentialMustDifferFromOthers(t *testing.T) {
	s := New(Config{})
	if err := s.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableGateway(testAdminToken); err == nil {
		t.Fatal("gateway credential equal to the admin credential accepted")
	}
	if err := s.EnableGateway("short"); err == nil {
		t.Fatal("invalid gateway credential accepted")
	}
	if err := s.EnableGateway(testGatewayToken); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableGateway(otherToken); err == nil {
		t.Fatal("gateway credential configured twice")
	}
	if err := s.EnableAccess(testGatewayToken); err == nil {
		t.Fatal("access credential equal to the gateway credential accepted")
	}
	other := New(Config{})
	if err := other.EnableGateway(testGatewayToken); err != nil {
		t.Fatal(err)
	}
	if err := other.EnableAdmin(testGatewayToken); err == nil {
		t.Fatal("admin credential equal to the gateway credential accepted")
	}
}

func TestFailedCredentialAddressTableIsBounded(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := New(Config{Now: clock.Now})
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range maxFailureAddresses + 10 {
		s.noteFailedAddressLocked(fmt.Sprintf("label:%d", i))
	}
	for range 2 * maxFailuresPerAddress {
		s.noteFailedAddressLocked("label:busy")
	}
	if len(s.failedAddresses) > maxFailureAddresses || len(s.failedAddresses["label:busy"]) != maxFailuresPerAddress {
		t.Fatalf("table grew: %d addresses, %d busy entries", len(s.failedAddresses), len(s.failedAddresses["label:busy"]))
	}
	clock.Advance(failureWindow)
	s.noteFailedAddressLocked("label:new")
	if len(s.failedAddresses) != 1 {
		t.Fatalf("expired addresses retained: %d", len(s.failedAddresses))
	}
}
