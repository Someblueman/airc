//go:build unix

package auth

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testtls"
)

func ircSessionFixture(t *testing.T, mode string) (*IRCServer, config.IRCBridge, chan struct{}) {
	t.Helper()
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	done := make(chan error, 1)
	entered := make(chan struct{})
	security := cert.Server.Clone()
	if mode == "TLS12" {
		security.MinVersion = tls.VersionTLS12
		security.MaxVersion = tls.VersionTLS12
	}
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		accepted <- raw
		runIRCScript(raw, security, mode, entered, done)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("scripted IRC server not joined")
		}
	})
	c, _ := fixtureConfig(t)
	c.Principals[0].Operations = []core.Operation{core.Send}
	password := filepath.Join(privateTemp(t), "irc-password")
	if err = os.WriteFile(password, []byte("exact-password"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.IRCBridge{ID: "network", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Nickname: "bridge", Account: "bridge-account", PasswordFile: password, FreshnessSeconds: 60, Routes: []config.IRCRoute{{Account: "alice", Room: "#external", Principal: "device", Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	if mode == "join_unicode_nick" {
		b.Nickname = "keeper"
	}
	if mode == "join_unicode_room" {
		b.Routes[0].Room = "#keeper"
	}
	if mode == "join_duplicate" {
		route := b.Routes[0]
		route.Account = "bob"
		b.Routes = append(b.Routes, route)
	}
	if mode == "join_multiple" {
		route := b.Routes[0]
		route.Room = "#other"
		b.Routes = append(b.Routes, route)
	}
	if mode == "wrong_name" {
		b.TLSServerName = "other.example.com"
	}
	if mode == "unknown_ca" {
		b.TLSCAFile = testtls.New(t).CAPath
	}
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewIRCServer(v, c, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, b, entered
}
