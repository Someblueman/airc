package irc_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestKickMonitorAndAwayAcrossReconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := server.New(server.Config{HistoryLimit: 32, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	token := strings.Repeat("a", 64)
	if err := s.EnableAdmin(token); err != nil {
		t.Fatal(err)
	}
	go s.Serve(listener)
	defer s.Shutdown(context.Background())
	addr := listener.Addr().String()
	c, err := irc.Dial(irc.Config{Nick: "bot", Addr: addr, Reconnect: true, MinBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Join("#kicked")
	c.Join("#keep")
	c.Away("calculating")
	c.Monitor("alice")
	nextEvent(t, c, func(e irc.Event) bool { m, ok := e.(*irc.MonitorEvent); return ok && !m.Online })
	admin := newClient(t, "admin", addr)
	admin.AuthenticateAdmin(token)
	admin.Kick("#kicked", "bot", "quiet")
	nextEvent(t, c, func(e irc.Event) bool { _, ok := e.(*irc.KickEvent); return ok })
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = startServer(t, addr)
	nextEvent(t, c, func(e irc.Event) bool { m, ok := e.(*irc.ConnectionEvent); return ok && m.Connected })
	c.Raw("WHOIS bot")
	nextEvent(t, c, func(e irc.Event) bool {
		r, ok := e.(*irc.RawEvent)
		return ok && r.Command == "301" && r.Trailing == "calculating"
	})
	e := nextEvent(t, c, func(e irc.Event) bool { r, ok := e.(*irc.RawEvent); return ok && r.Command == "319" }).(*irc.RawEvent)
	if e.Trailing != "#keep" {
		t.Fatalf("kick was undone on reconnect: %s", e.Trailing)
	}
	alice := newClient(t, "alice", addr)
	defer alice.Close()
	nextEvent(t, c, func(e irc.Event) bool { m, ok := e.(*irc.MonitorEvent); return ok && m.Online && m.Nicks[0] == "alice" })
}
