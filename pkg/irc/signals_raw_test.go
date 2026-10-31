package irc_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestObserveSignalsRawPreserves780AndLeaves777Typed(t *testing.T) {
	client := startSignalFixture(t)
	if err := client.ObserveSignalsRaw("#room"); err != nil {
		t.Fatal(err)
	}

	var first *irc.RawEvent
	for first == nil {
		event := nextSignalEvent(t, client)
		if raw, ok := event.(*irc.RawEvent); ok && raw.Command == "780" {
			first = raw
		}
	}
	if first.Prefix != "server" || len(first.Params) != 1 || first.Params[0] != "reader" || first.Trailing != `{"action":"typing","target":"#room","from":"peer"}` || first.Tags["trace"] != "one" {
		t.Fatalf("unexpected raw 780: %#v", first)
	}
	// The event owns copies of mutable wire fields.
	first.Params[0] = "changed"
	first.Tags["trace"] = "changed"

	var malformed *irc.RawEvent
	var chat *irc.ChatEvent
	for malformed == nil || chat == nil {
		event := nextSignalEvent(t, client)
		switch value := event.(type) {
		case *irc.RawEvent:
			if value.Command == "780" {
				malformed = value
			}
		case *irc.ChatEvent:
			chat = value
		}
	}
	if malformed.Trailing != "{bad-json" || malformed.Params[0] != "reader" || malformed.Tags["trace"] != "two" {
		t.Fatalf("raw 780 was not preserved or copied: %#v", malformed)
	}
	if chat.ChatEntry.Action != "message" || chat.ChatEntry.Target != "#room" {
		t.Fatalf("777 changed in raw mode: %#v", chat)
	}
}

func TestObserveSelectsTypedSignalsAndChat(t *testing.T) {
	client := startSignalFixture(t)
	if err := client.Observe("#room"); err != nil {
		t.Fatal(err)
	}
	var signal *irc.SignalEvent
	var chat *irc.ChatEvent
	for signal == nil || chat == nil {
		event := nextSignalEvent(t, client)
		switch value := event.(type) {
		case *irc.SignalEvent:
			signal = value
		case *irc.ChatEvent:
			chat = value
		}
	}
	if signal.ChatEntry.Action != "typing" || chat.ChatEntry.Action != "message" {
		t.Fatalf("unexpected typed events: %#v %#v", signal, chat)
	}
}

func startSignalFixture(t *testing.T) *irc.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				fmt.Fprintf(conn, ":server 005 reader AIRC=%d :features\r\n:server 001 reader :ready\r\n", protocol.Level)
			}
			if strings.HasPrefix(line, "OBSERVE ") {
				fmt.Fprint(conn,
					":server 765 reader #room :observing\r\n"+
						"@trace=one :server 780 reader :{\"action\":\"typing\",\"target\":\"#room\",\"from\":\"peer\"}\r\n"+
						"@trace=two :server 780 reader :{bad-json\r\n"+
						":peer 777 reader :{\"action\":\"message\",\"target\":\"#room\",\"message\":{\"id\":\"0123456789abcdef0123456789abcdef\",\"from\":\"peer\",\"target\":\"#room\",\"body_base64\":\"aGVsbG8\",\"timestamp\":\"2026-10-04T00:00:00Z\"}}\r\n")
				return
			}
		}
	}()
	client, err := irc.DialContext(context.Background(), irc.Config{Addr: listener.Addr().String(), Nick: "reader", ReadTimeout: time.Second})
	if err != nil {
		listener.Close()
		<-done
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		listener.Close()
		<-done
	})
	return client
}

func nextSignalEvent(t *testing.T, client *irc.Client) irc.Event {
	t.Helper()
	select {
	case event, ok := <-client.Events():
		if !ok {
			t.Fatal("signal fixture closed")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for signal event")
		return nil
	}
}
