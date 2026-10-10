//go:build darwin || linux

package service

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
	"github.com/Someblueman/airc/internal/testtls"
)

func ircServiceFixture(t *testing.T, publish func(*tls.Conn)) config.Config {
	c, _, _, _ := natsServiceConfig(t)
	c.Listeners = []config.Listener{}
	t.Helper()
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		conn := tls.Server(raw, cert.Server)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := ircwire.NewReader(conn)
		read := func(name string) error {
			command, err := reader.Read()
			if err != nil {
				return err
			}
			if command.Name != name {
				return errors.New("IRC client command mismatch")
			}
			return nil
		}
		write := func(line string) error { _, err := io.WriteString(conn, line+"\r\n"); return err }
		for _, name := range []string{"CAP", "NICK", "USER"} {
			if err := read(name); err != nil {
				done <- err
				return
			}
		}
		for _, step := range []struct{ line, name string }{
			{":server CAP bridge ACK :sasl account-tag message-tags server-time", "AUTHENTICATE"},
			{"AUTHENTICATE +", "AUTHENTICATE"},
			{":server 900 bridge bridge!u@h bridge-account :logged in\r\n:server 903 bridge :success", "CAP"},
			{":server 001 bridge :welcome", "JOIN"},
		} {
			if err := write(step.line); err != nil {
				done <- err
				return
			}
			if err := read(step.name); err != nil {
				done <- err
				return
			}
		}
		if err := write(":bridge!u@h JOIN #external"); err != nil {
			done <- err
			return
		}
		publish(conn)
		_, _ = io.Copy(io.Discard, conn)
		done <- nil
	}()
	password := filepath.Join(filepath.Dir(c.StateDir), "irc-password")
	if err := os.WriteFile(password, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.IRCBridge{ID: "network", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Nickname: "bridge", Account: "bridge-account", PasswordFile: password, FreshnessSeconds: 60, Routes: []config.IRCRoute{{Account: "alice", Room: "#external", Principal: c.Principals[0].ID, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	b.Enabled = true
	c.IRCBridges = []config.IRCBridge{b}
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error("IRC peer", err)
			}
		case <-time.After(time.Second):
			t.Error("IRC peer not joined")
		}
	})
	return c
}
