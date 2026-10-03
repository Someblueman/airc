package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
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

func accessServer(t *testing.T, cfg Config) (*Server, net.Listener) {
	t.Helper()
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(cfg)
	if err := srv.EnableAccess(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return srv, listener
}

func TestAccessGatePrecedesAllRegistrationAndData(t *testing.T) {
	srv, listener := accessServer(t, Config{})
	conn, err := net.Dial("tcp", listener.Addr().String())
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
	_, listener := accessServer(t, Config{RegistrationTimeout: 80 * time.Millisecond})
	conn, err := net.Dial("tcp", listener.Addr().String())
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
	srv, listener := accessServer(t, Config{TLSConfig: cert.Server, MaxConnections: 1})
	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	deadline := time.Now().Add(5 * time.Second) // polls a condition; the deadline only bounds failure
	for {
		srv.mu.Lock()
		count := len(srv.clients)
		srv.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection not counted before handshake")
		}
		time.Sleep(time.Millisecond)
	}
	second, err := net.Dial("tcp", listener.Addr().String())
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
