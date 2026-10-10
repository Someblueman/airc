package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testtls"
)

type addressListener struct {
	net.Listener
	addr   net.Addr
	closed bool
}

func (l *addressListener) Addr() net.Addr { return l.addr }
func (l *addressListener) Close() error   { l.closed = true; return nil }

func TestServeRefusesUnprotectedRemoteBind(t *testing.T) {
	for _, cfg := range []Config{{}, {TLSConfig: &tls.Config{}}} {
		srv := New(cfg)
		listener := &addressListener{addr: &net.TCPAddr{IP: net.IPv4zero, Port: 6667}}
		if err := srv.Serve(listener); err == nil || !listener.closed {
			t.Fatal("remote listener accepted without required protections")
		}
	}
	srv := New(Config{})
	if err := srv.EnableAccess(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	listener := &addressListener{addr: &net.TCPAddr{IP: net.ParseIP("::"), Port: 6667}}
	if err := srv.Serve(listener); err == nil {
		t.Fatal("access token accepted without TLS")
	}
}

func TestAccessGatePrecedesAllRegistrationAndData(t *testing.T) {
	address, srv := accessServer(t, Config{})
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	scanner := bufio.NewScanner(conn)
	for _, command := range []string{"NICK attacker", "USER attacker 0 * :attacker", "AUTH victim :" + strings.Repeat("b", 64), "REGISTER victim :" + strings.Repeat("b", 64), "EPHEMERAL", "STATUS", "HISTORY #room", "OBSERVE @*", "PASS :" + strings.Repeat("b", 64)} {
		if _, err := io.WriteString(conn, command+"\r\n"); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() || !strings.Contains(scanner.Text(), " 464 ") {
			t.Fatal(command, scanner.Text(), scanner.Err())
		}
	}
	srv.mu.Lock()
	nicks, accounts := len(srv.nicks), len(srv.accounts)
	srv.mu.Unlock()
	if nicks != 0 || accounts != 0 {
		t.Fatal("unauthorized registration changed server state")
	}
	io.WriteString(conn, "PASS :"+strings.Repeat("a", 64)+"\r\n")
	if !scanner.Scan() || !strings.Contains(scanner.Text(), " 782 ") {
		t.Fatal(scanner.Text())
	}
	if err := srv.EnableAdmin(strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted access credential as operator")
	}
}

func TestRegistrationDeadlineCannotBeExtendedByPing(t *testing.T) {
	address, _ := accessServer(t, Config{RegistrationTimeout: 80 * time.Millisecond})
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	closed := make(chan struct{})
	go func() { io.Copy(io.Discard, conn); close(closed) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(500 * time.Millisecond)
	defer timeout.Stop()
	for {
		select {
		case <-closed:
			return
		case <-ticker.C:
			io.WriteString(conn, "PING :keepalive\r\n")
		case <-timeout.C:
			t.Fatal("unregistered client extended its deadline")
		}
	}
}

func TestTLSHandshakeSlotsAreBoundedAndShutdownClosesThem(t *testing.T) {
	cert := testtls.New(t)
	address, srv := accessServer(t, Config{TLSConfig: cert.Server, MaxConnections: 1})
	first, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	eventually(t, "connection not counted before handshake", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.clients) == 1
	})
	second, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	second.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := second.Read(b[:]); err == nil {
		t.Fatal("full TLS listener kept a new handshake slot")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	first.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := first.Read(b[:]); err == nil {
		t.Fatal("shutdown left TLS handshake open")
	}
}

func TestAccessAndOperatorCredentialsMustDiffer(t *testing.T) {
	token := strings.Repeat("a", 64)
	first := New(Config{})
	if err := first.EnableAccess(token); err != nil {
		t.Fatal(err)
	}
	if err := first.EnableAdmin(token); err == nil {
		t.Fatal("access credential grants admin")
	}
	second := New(Config{})
	if err := second.EnableAdmin(token); err != nil {
		t.Fatal(err)
	}
	if err := second.EnableAccess(token); err == nil {
		t.Fatal("admin credential grants access")
	}
}

const (
	otherToken  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	wrongToken  = "00000000000000000000000000000000000000000000000000000000000000ff"
	failureText = "too many failed credential attempts"
)

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
			name:    "access token",
			server:  func(t *testing.T) string { a, _ := accessServer(t, Config{}); return a },
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
