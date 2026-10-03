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

func TestBusySenderCannotFillQueueOrStarveAnotherSender(t *testing.T) {
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
	cfg := irc.Config{Nick: "utility", Addr: l.Addr().String(), IdentityToken: strings.Repeat("a", 64), Ephemeral: true, CreateAccount: true}
	account, err := irc.DialContext(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	account.Close()
	cfg.Ephemeral = false
	cfg.CreateAccount = false
	alice, err := irc.DialContext(ctx, irc.Config{Nick: "alice", Addr: cfg.Addr})
	if err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	alice.Join("#room")
	bob, err := irc.DialContext(ctx, irc.Config{Nick: "bob", Addr: cfg.Addr})
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	bob.Join("#room")
	started := make(chan struct{})
	release := make(chan struct{})
	calls := make(chan string, 8)
	handler := Command{Name: "work", Handle: func(ctx context.Context, _ *irc.MessageEvent, arg string) (string, error) {
		calls <- arg
		if arg == "first" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return arg, nil
	}}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(runCtx, Config{Client: cfg, Channels: []string{"#room"}, Commands: []Command{handler}, Interval: time.Millisecond})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("bot never joined")
		case e := <-alice.Events():
			if j, ok := e.(*irc.JoinEvent); ok && j.Agent == "utility" {
				goto joined
			}
		}
	}
joined:
	alice.Send("#room", "utility: work first")
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	alice.Send("#room", "utility: work queued")
	alice.Send("#room", "utility: work excess")
	bob.Send("#room", "utility: work other")
	for {
		select {
		case <-ctx.Done():
			t.Fatal("busy feedback missing")
		case e := <-alice.Events():
			if m, ok := e.(*irc.MessageEvent); ok && m.From == "utility" && strings.Contains(m.Message, "Busy:") {
				goto busy
			}
		}
	}
busy:
	close(release)
	got := []string{}
	for len(got) < 3 {
		select {
		case arg := <-calls:
			got = append(got, arg)
		case <-ctx.Done():
			t.Fatal("another sender was starved", got)
		}
	}
	if strings.Join(got, ",") != "first,queued,other" {
		t.Fatal("queue was not fair/bounded", got)
	}
	select {
	case arg := <-calls:
		t.Fatal("excess command executed", arg)
	case <-time.After(30 * time.Millisecond):
	}
}
