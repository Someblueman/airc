package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func cliTestServer(t *testing.T) string {
	t.Helper()
	return cliTestServerWith(t, server.Config{HistoryLimit: 16})
}

func cliTestServerWith(t *testing.T, cfg server.Config) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PingInterval, cfg.ReadTimeout = time.Hour, time.Hour
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(cfg)
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return listener.Addr().String()
}

func TestSendJSONConfirmsChannelAndDirectMessages(t *testing.T) {
	address := cliTestServer(t)
	recipient, err := irc.Dial(irc.Config{Nick: "builder", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer recipient.Close()
	for _, target := range []struct {
		args           []string
		expected, body string
	}{
		{[]string{"send", "--nick", "writer", "--channel", "#cli", "--message", "hello channel", "--addr", address, "--json"}, "#cli", "hello channel"},
		{[]string{"send", "--nick", "planner", "--to", "builder", "--message", "private request", "--addr", address, "--json"}, "builder", "private request"},
	} {
		var stdout, stderr bytes.Buffer
		if err := run(target.args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatalf("run send: %v (%s)", err, stderr.String())
		}
		var message irc.MessageEvent
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &message); err != nil {
			t.Fatal(err)
		}
		if message.Target != target.expected || message.Message != target.body || message.ID == "" {
			t.Fatalf("unexpected send response: %#v", message)
		}
	}
	var historyOut, historyErr bytes.Buffer
	if err := run([]string{"history", "#cli", "--nick", "reader", "--addr", address, "--json"}, strings.NewReader(""), &historyOut, &historyErr); err != nil {
		t.Fatalf("run history: %v (%s)", err, historyErr.String())
	}
	if !strings.Contains(historyOut.String(), `"message":"hello channel"`) {
		t.Fatalf("history omitted channel message: %s", historyOut.String())
	}
}

func TestAgentsJSONIncludesLivePresence(t *testing.T) {
	address := cliTestServer(t)
	bot, err := irc.Dial(irc.Config{Nick: "builder", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer bot.Close()
	if err := bot.Join("#build"); err != nil {
		t.Fatal(err)
	}
	joinTimer := time.NewTimer(3 * time.Second)
	defer joinTimer.Stop()
	for {
		select {
		case event := <-bot.Events():
			if joined, ok := event.(*irc.JoinEvent); ok && joined.Channel == "#build" {
				goto joined
			}
		case <-joinTimer.C:
			t.Fatal("bot did not join channel")
		}
	}
joined:
	var stdout, stderr bytes.Buffer
	args := []string{"agents", "--nick", "observer", "--addr", address, "--json"}
	if err := run(args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run agents: %v (%s)", err, stderr.String())
	}
	var agents []irc.AgentInfo
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &agents); err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.Nick == "builder" {
			if len(agent.Channels) != 1 || agent.Channels[0] != "#build" {
				t.Fatalf("unexpected builder presence: %#v", agent)
			}
			return
		}
	}
	t.Fatalf("agents response omitted builder: %s", stdout.String())
}

func TestFormatWatchMessageUsesTimestampAndWrapsLongText(t *testing.T) {
	timestamp := time.Date(2026, time.September, 30, 12, 34, 56, 0, time.UTC)
	message := &irc.MessageEvent{
		From: "flash", Message: strings.Repeat("hello from the watcher test ", 5) + strings.Repeat("x", 90), Timestamp: timestamp,
	}
	formatted := formatWatchMessage(message)
	wantHeader := "[" + timestamp.Local().Format("2006-01-02 15:04:05") + "] flash\n"
	if !strings.Contains(formatted, wantHeader) {
		t.Fatalf("formatted message lacks timestamp and speaker header %q: %q", wantHeader, formatted)
	}
	lines := strings.Split(strings.TrimSpace(formatted), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[1], "  ") {
		t.Fatalf("message body was not placed under an indented header: %q", formatted)
	}
	for _, line := range lines[1:] {
		if len([]rune(line)) > 80 {
			t.Errorf("watch output line exceeds 80 characters: %d: %q", len([]rune(line)), line)
		}
	}
}

func TestWaitForObservationWaitsForServerAcknowledgement(t *testing.T) {
	address := cliTestServer(t)
	client, err := irc.Dial(irc.Config{Nick: "watcher", Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Raw("OBSERVE #watch"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitForObservation(ctx, client, "#watch"); err != nil {
		t.Fatalf("waitForObservation: %v", err)
	}
}
