package irc_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func startServer(t *testing.T, address string) (*server.Server, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{HistoryLimit: 32, PingInterval: time.Hour, ReadTimeout: time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown server: %v", err)
		}
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("serve server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server accept loop did not stop")
		}
	})
	return srv, listener
}

func newClient(t *testing.T, nick, address string) *irc.Client {
	t.Helper()
	client, err := irc.Dial(irc.Config{Nick: nick, Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close %s: %v", nick, err)
		}
	})
	return client
}

func nextEvent(t *testing.T, client *irc.Client, match func(irc.Event) bool) irc.Event {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				t.Fatal("client event stream closed")
			}
			if match(event) {
				return event
			}
		case <-timer.C:
			t.Fatal("timed out waiting for client event")
		}
	}
}

func TestChannelDirectMessagingPresenceAndHistory(t *testing.T) {
	_, listener := startServer(t, "127.0.0.1:0")
	address := listener.Addr().String()
	alice := newClient(t, "alice", address)
	bob := newClient(t, "builder", address)
	reviewer := newClient(t, "reviewer", address)
	if duplicate, err := irc.Dial(irc.Config{Nick: "BUILDER", Addr: address}); err == nil {
		_ = duplicate.Close()
		t.Fatal("server accepted a case-insensitive duplicate nickname")
	}
	for _, client := range []*irc.Client{alice, bob, reviewer} {
		if err := client.Join("#project"); err != nil {
			t.Fatal(err)
		}
		nick := map[*irc.Client]string{alice: "alice", bob: "builder", reviewer: "reviewer"}[client]
		nextEvent(t, client, func(event irc.Event) bool {
			joined, ok := event.(*irc.JoinEvent)
			return ok && joined.Agent == nick && joined.Channel == "#project"
		})
	}

	if err := alice.Send("#project", "implementation ready"); err != nil {
		t.Fatal(err)
	}
	for _, client := range []*irc.Client{alice, bob, reviewer} {
		message := nextEvent(t, client, func(event irc.Event) bool {
			value, ok := event.(*irc.MessageEvent)
			return ok && value.From == "alice" && value.Target == "#project" && value.Message == "implementation ready" && value.ID != ""
		}).(*irc.MessageEvent)
		if message.Timestamp.IsZero() {
			t.Fatalf("missing server timestamp: %#v", message)
		}
	}

	if err := alice.Send("builder", "please review this"); err != nil {
		t.Fatal(err)
	}
	direct := nextEvent(t, bob, func(event irc.Event) bool {
		value, ok := event.(*irc.MessageEvent)
		return ok && value.From == "alice" && value.Target == "builder" && value.Message == "please review this"
	}).(*irc.MessageEvent)
	if direct.ID == "" {
		t.Fatal("direct message is missing message id")
	}
	nextEvent(t, alice, func(event irc.Event) bool {
		value, ok := event.(*irc.SendReceiptEvent)
		return ok && value.ID == direct.ID
	})
	select {
	case event := <-reviewer.Events():
		if message, ok := event.(*irc.MessageEvent); ok && message.Message == "please review this" {
			t.Fatal("private message reached the reviewer")
		}
	case <-time.After(100 * time.Millisecond):
	}
	if err := bob.Part("#project", "review requested"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, bob, func(event irc.Event) bool {
		value, ok := event.(*irc.PartEvent)
		return ok && value.Agent == "builder" && value.Channel == "#project"
	})
	nextEvent(t, reviewer, func(event irc.Event) bool {
		value, ok := event.(*irc.PartEvent)
		return ok && value.Agent == "builder" && value.Channel == "#project"
	})

	observer := newClient(t, "observer", address)
	if err := observer.Join("#project"); err != nil {
		t.Fatal(err)
	}
	if err := observer.History("#project", 10); err != nil {
		t.Fatal(err)
	}
	history := nextEvent(t, observer, func(event irc.Event) bool {
		value, ok := event.(*irc.HistoryEvent)
		return ok && value.Message == "implementation ready"
	}).(*irc.HistoryEvent)
	if history.ID == "" || history.Timestamp.IsZero() {
		t.Fatalf("incomplete history event: %#v", history)
	}
	nextEvent(t, observer, func(event irc.Event) bool { _, ok := event.(*irc.EndOfHistoryEvent); return ok })

	if err := observer.Raw("AGENTS"); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for {
		event := nextEvent(t, observer, func(event irc.Event) bool {
			_, isAgent := event.(*irc.AgentsEvent)
			_, isEnd := event.(*irc.EndOfAgentsEvent)
			return isAgent || isEnd
		})
		if value, ok := event.(*irc.AgentsEvent); ok {
			seen[value.Agent.Nick] = true
			if value.Agent.Nick == "alice" && len(value.Agent.Channels) != 1 {
				t.Fatalf("presence lacks channels: %#v", value.Agent)
			}
			continue
		}
		break
	}
	for _, nick := range []string{"alice", "builder", "reviewer", "observer"} {
		if !seen[nick] {
			t.Errorf("AGENTS omitted %s", nick)
		}
	}
	if err := reviewer.Close(); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, observer, func(event irc.Event) bool {
		value, ok := event.(*irc.QuitEvent)
		return ok && value.Agent == "reviewer"
	})
	if err := observer.Raw("AGENTS"); err != nil {
		t.Fatal(err)
	}
	for {
		event := nextEvent(t, observer, func(event irc.Event) bool {
			_, isAgent := event.(*irc.AgentsEvent)
			_, isEnd := event.(*irc.EndOfAgentsEvent)
			return isAgent || isEnd
		})
		if value, ok := event.(*irc.AgentsEvent); ok && value.Agent.Nick == "reviewer" {
			t.Fatal("disconnected reviewer remained in presence list")
		}
		if _, ok := event.(*irc.EndOfAgentsEvent); ok {
			break
		}
	}
}

func TestReconnectReclaimsNicknameAndRejoinsChannels(t *testing.T) {
	firstServer, firstListener := startServer(t, "127.0.0.1:0")
	address := firstListener.Addr().String()
	client, err := irc.Dial(irc.Config{Nick: "researcher", Addr: address, Reconnect: true, MinBackoff: 25 * time.Millisecond, MaxBackoff: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Join("#research"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.JoinEvent)
		return ok && value.Agent == "researcher" && value.Channel == "#research"
	})
	if err := client.SetNick("researcher2"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.NickEvent)
		return ok && value.Old == "researcher" && value.Nick == "researcher2"
	})
	if client.Nick() != "researcher2" {
		t.Fatalf("client Nick() = %q after rename", client.Nick())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := firstServer.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	shutdownCancel()
	listener2, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	srv2 := server.New(server.Config{PingInterval: time.Hour, ReadTimeout: time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv2.Serve(listener2) }()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		if err := srv2.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown replacement server: %v", err)
		}
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Error("replacement accept loop did not stop")
		}
	})
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.JoinEvent)
		return ok && value.Agent == "researcher2" && value.Channel == "#research"
	})
}

func TestClientRejectsLineInjection(t *testing.T) {
	if _, err := irc.Dial(irc.Config{Nick: "safe\r\nNICK attacker", Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("accepted injected nickname")
	}
	client := &irc.Client{}
	if err := client.Send("#room", "hello\r\nPRIVMSG victim :leak"); err == nil {
		t.Fatal("accepted injected message")
	}
}

func TestUnixSocketUsesSameClientProtocol(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "airc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	listener, err := server.ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{PingInterval: time.Hour, ReadTimeout: time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown unix server: %v", err)
		}
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("serve unix server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("unix accept loop did not stop")
		}
	})
	client, err := irc.Dial(irc.Config{Nick: "local", Network: "unix", Addr: path})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Join("#local"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, client, func(event irc.Event) bool {
		value, ok := event.(*irc.JoinEvent)
		return ok && value.Channel == "#local" && value.Agent == "local"
	})
}

func TestConcurrentChannelBroadcasts(t *testing.T) {
	_, listener := startServer(t, "127.0.0.1:0")
	const count = 6
	clients := make([]*irc.Client, count)
	for i := range clients {
		clients[i] = newClient(t, "agent"+string(rune('a'+i)), listener.Addr().String())
		if err := clients[i].Join("#concurrent"); err != nil {
			t.Fatal(err)
		}
		nick := clients[i].Nick()
		nextEvent(t, clients[i], func(event irc.Event) bool {
			joined, ok := event.(*irc.JoinEvent)
			return ok && joined.Agent == nick && joined.Channel == "#concurrent"
		})
	}
	var writers sync.WaitGroup
	errCh := make(chan error, count)
	for i, client := range clients {
		writers.Add(1)
		go func(index int, sender *irc.Client) {
			defer writers.Done()
			errCh <- sender.Send("#concurrent", "parallel-"+string(rune('a'+index)))
		}(i, client)
	}
	writers.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, client := range clients {
		seen := make(map[string]bool)
		for len(seen) < count {
			message := nextEvent(t, client, func(event irc.Event) bool {
				_, ok := event.(*irc.MessageEvent)
				return ok
			}).(*irc.MessageEvent)
			seen[message.Message] = true
		}
		if len(seen) != count {
			t.Fatalf("received %d distinct messages", len(seen))
		}
	}
}
