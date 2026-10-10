//go:build darwin || linux

package adapter

import (
	"context"
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
	"github.com/Someblueman/airc/internal/gateway/ircwire"
	"github.com/Someblueman/airc/internal/testtls"
)

func TestIRCOwnerRegistrationFailureBackoffAndJoinedCancellation(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	cert := testtls.New(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	attempts := make(chan time.Time, 3)
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		for range 3 {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			conn := tls.Server(raw, cert.Server)
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			command, err := ircwire.NewReader(conn).Read()
			if err == nil && command.Name == "CAP" {
				attempts <- time.Now()
			}
			_, _ = io.WriteString(conn, "ERROR :registration refused\r\n")
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-peerDone })
	password := filepath.Join(filepath.Dir(f.c.StateDir), "irc-password")
	if err := os.WriteFile(password, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	b := config.IRCBridge{ID: "network", Address: listener.Addr().String(), TLSServerName: "localhost", TLSCAFile: cert.CAPath, Nickname: "bridge", Account: "bridge-account", PasswordFile: password, FreshnessSeconds: 60, Routes: []config.IRCRoute{{Account: "alice", Room: "#external", Principal: "producer", Target: f.c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	server, err := auth.NewIRCServer(f.v, f.c, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(t.Context())
	slots, requests := make(chan struct{}, 1), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- ServeIRC(ctx, server, f.d, slots, requests) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	var previous time.Time
	for i := range 3 {
		select {
		case attempt := <-attempts:
			if i > 0 && attempt.Sub(previous) < 100*time.Millisecond*time.Duration(1<<(i-1)) {
				t.Fatal("failure skipped backoff")
			}
			previous = attempt
		case <-time.After(2 * time.Second):
			t.Fatal("missing retry")
		}
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("backoff not joined")
	}
	if len(slots) != 0 || len(requests) != 0 {
		t.Fatal("failed connection leaked capacity")
	}
}
