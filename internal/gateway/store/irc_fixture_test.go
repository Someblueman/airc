//go:build darwin || linux

package store

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

// This factory uses a real verified TLS session and public event reader, never
// constructs or supplies parsed commands to mint an authentication proof.
func ircProof(t *testing.T, c config.Config, principal, id, body string, stamp time.Time) auth.VerifiedIRCEvent {
	t.Helper()
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		conn := tls.Server(raw, cert.Server)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		reader := bufio.NewReader(conn)
		read := func() error { _, err := reader.ReadString('\n'); return err }
		write := func(s string) error { _, err := io.WriteString(conn, s+"\r\n"); return err }
		for range 3 {
			if err := read(); err != nil {
				done <- err
				return
			}
		}
		if err := write(":server CAP bridge ACK :sasl account-tag message-tags server-time"); err != nil {
			done <- err
			return
		}
		if err := read(); err != nil {
			done <- err
			return
		}
		if err := write("AUTHENTICATE +"); err != nil {
			done <- err
			return
		}
		if err := read(); err != nil {
			done <- err
			return
		}
		for _, s := range []string{":server 900 bridge bridge!u@h bridge-account :logged in", ":server 903 bridge :success"} {
			if err := write(s); err != nil {
				done <- err
				return
			}
		}
		if err := read(); err != nil {
			done <- err
			return
		}
		if err := write(":server 001 bridge :welcome"); err != nil {
			done <- err
			return
		}
		done <- write("@account=alice;msgid=" + id + ";time=" + stamp.UTC().Format("2006-01-02T15:04:05.000Z") + " :alice!u@h PRIVMSG #external :" + body)
	}()
	password := filepath.Join(filepath.Dir(c.StateDir), "irc-password")
	if err := os.WriteFile(password, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.IRCBridge{ID: "network", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Nickname: "bridge", Account: "bridge-account", PasswordFile: password, FreshnessSeconds: 60, Routes: []config.IRCRoute{{Account: "alice", Room: "#external", Principal: principal, Target: core.Target{Kind: "room", Name: "#Allowed"}, Operations: []core.Operation{core.Send}}}}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	server, err := auth.NewIRCServer(v, c, b)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	session, err := server.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	proof, err := session.ReadEvent()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return proof
}
