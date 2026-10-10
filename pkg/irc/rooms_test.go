package irc_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestKickMonitorAndAwayAcrossReconnect(t *testing.T) {
	token := strings.Repeat("a", 64)
	var s *server.Server
	addr := startServer(t, server.Config{HistoryLimit: 32}, func(srv *server.Server) error {
		s = srv
		return srv.EnableAdmin(token)
	})
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
	restartServer(t, addr)
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
