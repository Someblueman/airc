package irc_test

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMultilineMessagesRoundTripAndDegradeForPlainClients(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 16}, nil)
	alice := newClient(t, "alice", address)
	bob := newClient(t, "bob", address)
	if !alice.Multiline() {
		t.Fatal("server did not advertise multi-line support")
	}
	joinAndWait(t, alice, "#room")
	joinAndWait(t, bob, "#room")
	// A plain IRC client (nc, irssi) knows nothing about the tag.
	plain, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	io.WriteString(plain, "NICK plain\r\nUSER p 0 * :p\r\nJOIN #room\r\n")
	reader := bufio.NewReader(plain)
	plain.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("plain client never finished joining: %v", err)
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}

	body := "Status report\n\n- built: yes\n- tests: passing ✓\r\nDone."
	want := "Status report\n\n- built: yes\n- tests: passing ✓\nDone."
	if err := alice.Send("#room", body); err != nil {
		t.Fatal(err)
	}
	got := nextEvent(t, bob, func(event irc.Event) bool {
		m, ok := event.(*irc.MessageEvent)
		return ok && m.From == "alice"
	}).(*irc.MessageEvent)
	if got.Message != want {
		t.Fatalf("bob received %q, want %q", got.Message, want)
	}
	echo := nextEvent(t, alice, func(event irc.Event) bool {
		m, ok := event.(*irc.MessageEvent)
		return ok && m.From == "alice"
	}).(*irc.MessageEvent)
	if echo.Message != want {
		t.Fatalf("sender echo = %q", echo.Message)
	}

	plain.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("plain client never saw the message: %v", err)
		}
		if strings.Contains(line, "PRIVMSG #room") {
			if !strings.HasSuffix(line, ":Status report ⏎ - built: yes ⏎ - tests: passing ✓ ⏎ Done.\r\n") {
				t.Fatalf("plain client line = %q", line)
			}
			break
		}
	}

	reader2 := dialOneShot(t, "reader", address)
	if msgs, _ := readHistory(t, reader2, "#room", "", 5); len(msgs) != 1 || msgs[0].Message != want {
		t.Fatalf("history body = %v", bodies(msgs))
	}
	// Direct messages queue with their line breaks intact too.
	if receipt := sendOneShot(t, "alice-cli", address, "carol", "a\nb"); !receipt.Queued || receipt.Message != "a\nb" {
		t.Fatalf("queued multi-line receipt = %#v", receipt)
	}
	if msgs, _ := readHistory(t, reader2, "carol", "", 5); len(msgs) != 1 || msgs[0].Message != "a\nb" {
		t.Fatalf("inbox body = %v", bodies(msgs))
	}
}

func TestMultilineBodiesCannotInjectProtocolCommands(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 16}, nil)
	alice := newClient(t, "alice", address)
	bob := newClient(t, "bob", address)
	joinAndWait(t, alice, "#room")
	joinAndWait(t, bob, "#room")
	attack := "hi\r\nQUIT :gone\r\nPRIVMSG #room :forged\nJOIN #elsewhere"
	if err := alice.Send("#room", attack); err != nil {
		t.Fatal(err)
	}
	got := nextEvent(t, bob, func(event irc.Event) bool { _, ok := event.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if got.Message != "hi\nQUIT :gone\nPRIVMSG #room :forged\nJOIN #elsewhere" || got.From != "alice" {
		t.Fatalf("body was not delivered as plain text: %#v", got)
	}
	// alice is still connected and in the room: the text was never run as commands.
	if err := alice.Send("#room", "still here"); err != nil {
		t.Fatal(err)
	}
	next := nextEvent(t, bob, func(event irc.Event) bool { _, ok := event.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if next.Message != "still here" {
		t.Fatalf("unexpected follow-up %#v", next)
	}
}
