package bot

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestCommandBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text, from, target, kind, typ string
		want                          bool
	}{
		{"utility: calc 2+2", "alice", "#room", "", "message", true},
		{"@UTILITY help", "alice", "#room", "", "message", true},
		{"calc 2+2", "alice", "utility", "", "message", true},
		{"someone said utility: ping", "alice", "#room", "", "message", false},
		{"@utility-other ping", "alice", "#room", "", "message", false},
		{"utility: ping", "utility", "#room", "", "message", false},
		{"utility: ping", "alice", "#room", "bot", "message", false},
		{"utility: ping", "alice", "#room", "notice", "notice", false},
		{"utility: ping", "alice", "#room", "", "history", false},
	} {
		_, ok := commandText("utility", &irc.MessageEvent{Type: tc.typ, From: tc.from, Target: tc.target, Message: tc.text, Kind: tc.kind})
		if ok != tc.want {
			t.Fatalf("%+v: %t", tc, ok)
		}
	}
	calc := UtilityCommands()[1]
	for _, tc := range []struct {
		input, want string
		bad         bool
	}{{"(2+3)*4", "20", false}, {"1/0", "", true}, {"os.Exit(1)", "", true}, {"1e308*1e308", "", true}} {
		result, err := calc.Handle(context.Background(), nil, tc.input)
		if (err != nil) != tc.bad || result != tc.want {
			t.Fatalf("%s: %q %v", tc.input, result, err)
		}
	}
}

func TestLiveBotRepliesWithoutExecutingReplayOrNotice(t *testing.T) {
	s := server.New(server.Config{HistoryLimit: 64, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := s.RestoreAccounts(filepath.Join(t.TempDir(), "accounts")); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	defer s.Shutdown(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := irc.Config{Addr: l.Addr().String(), Nick: "utility", IdentityToken: strings.Repeat("a", 64), CreateAccount: true, Ephemeral: true}
	account, err := irc.DialContext(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	account.Close()
	user, err := irc.DialContext(ctx, irc.Config{Addr: l.Addr().String(), Nick: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	defer user.Close()
	if err := user.Join("#room"); err != nil {
		t.Fatal(err)
	}
	if err := user.Send("#room", "utility: calc 99+1"); err != nil {
		t.Fatal(err)
	}
	cfg.CreateAccount, cfg.Ephemeral = false, false
	botCtx, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		finished <- Run(botCtx, Config{Client: cfg, Channels: []string{"#room"}, Commands: UtilityCommands(), Interval: time.Nanosecond})
	}()
	defer func() {
		stop()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("bot failed to join")
		case e := <-user.Events():
			if j, ok := e.(*irc.JoinEvent); ok && j.Agent == "utility" {
				goto joined
			}
		}
	}
joined:
	user.Notice("#room", "utility: calc 9+9")
	user.Send("#room", "ordinary conversation")
	user.Send("#room", "utility: calc (2+3)*4")
	parent := ""
	for {
		select {
		case <-ctx.Done():
			t.Fatal("bot did not reply")
		case e := <-user.Events():
			m, ok := e.(*irc.MessageEvent)
			if !ok {
				continue
			}
			if m.From == "alice" && m.Message == "utility: calc (2+3)*4" {
				parent = m.ID
			}
			if m.From == "utility" {
				if m.Message != "20" || m.Kind != "bot" || m.ReplyTo != parent || m.ThreadID != parent {
					t.Fatalf("unexpected bot reply: %+v", m)
				}
				return
			}
		}
	}
}
