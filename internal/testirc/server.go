// Package testirc is a scripted IRC server for bridge tests. It confirms the
// bridge's TLS connection, SASL login as bridge-account, the account-tag
// capability and its join of #external, then hands the connection to the
// test.
package testirc

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
	"github.com/Someblueman/airc/internal/testtls"
)

// Serve accepts bridge connections until the test ends. Each registered
// connection is passed to session, which may write events and read replies;
// afterwards the server answers keepalive PINGs until the bridge disconnects.
// The returned bridge (ID "network", nickname "bridge") trusts the server and
// keeps its password in dir; the caller adds routes and enables it. At
// cleanup every session must have ended and every registration succeeded.
func Serve(t testing.TB, dir string, session func(*tls.Conn)) config.IRCBridge {
	t.Helper()
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var sessions sync.WaitGroup
	failures := make(chan error, 8)
	sessions.Go(func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					failures <- err
				}
				return
			}
			conn := tls.Server(raw, cert.Server)
			if err := register(conn); err != nil {
				failures <- err
				_ = conn.Close()
				continue
			}
			sessions.Go(func() {
				defer conn.Close()
				session(conn)
				reader := ircwire.NewReader(conn)
				for {
					command, err := reader.Read()
					if err != nil {
						return
					}
					if command.Name == "PING" {
						_, _ = io.WriteString(conn, ":server PONG server :airc-keepalive\r\n")
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		joined := make(chan struct{})
		go func() { sessions.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("IRC session left open")
		}
		close(failures)
		for err := range failures {
			t.Error("IRC registration", err)
		}
	})
	password := filepath.Join(dir, "irc-password")
	if err := os.WriteFile(password, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	return config.IRCBridge{ID: "network", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Nickname: "bridge", Account: "bridge-account", PasswordFile: password, FreshnessSeconds: 60}
}

// register runs the bridge's registration script.
func register(conn *tls.Conn) error {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := ircwire.NewReader(conn)
	read := func(name string) error {
		command, err := reader.Read()
		if err == nil && command.Name != name {
			err = fmt.Errorf("IRC client sent %s, want %s", command.Name, name)
		}
		return err
	}
	for _, name := range []string{"CAP", "NICK", "USER"} {
		if err := read(name); err != nil {
			return err
		}
	}
	for _, step := range []struct{ line, name string }{
		{":server CAP bridge ACK :sasl account-tag message-tags server-time", "AUTHENTICATE"},
		{"AUTHENTICATE +", "AUTHENTICATE"},
		{":server 900 * * bridge-account :logged in\r\n:server 903 * :success", "CAP"},
		{":server 001 bridge :welcome\r\n:server 422 bridge :MOTD File is missing", "JOIN"},
	} {
		if _, err := io.WriteString(conn, step.line+"\r\n"); err != nil {
			return err
		}
		if err := read(step.name); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(conn, ":bridge!u@h JOIN #external\r\n"); err != nil {
		return err
	}
	return conn.SetDeadline(time.Time{})
}
