package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestReplySignalCollectorRoomAndDirectDirections(t *testing.T) {
	tests := []struct {
		name, from, target, requested, native string
	}{
		{"room", "author", "#room", "#room", "#room"},
		{"direct incoming", "Peer", "Reader", "peer", "Reader"},
		{"direct outgoing", "Reader", "Peer", "peer", "Peer"},
		{"direct self", "Reader", "Reader", "reader", "Reader"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parent := strings.Repeat("a", 32)
			request := core.Request{Version: 1, RequestID: "signal-" + strings.ReplaceAll(tc.name, " ", "-"), Operation: core.ReplySignalSet, Target: &core.Target{Kind: map[bool]string{true: "room", false: "direct"}[strings.HasPrefix(tc.requested, "#")], Name: tc.requested}, ParentID: parent, ReplySignal: &core.ReplySignalInput{Note: "new", TTLSeconds: 30}}
			message := irc.MessageMetadata{ID: parent, Kind: "text", Target: tc.target, From: tc.from, Message: "question", Timestamp: time.Now().UTC()}
			proof := replySignalLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + replySignalLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
			ack := replySignalLine(t, irc.ChatEntry{Action: "prepare", Target: tc.native, ID: parent, From: "reader", AccountID: strings.Repeat("a", 32), Text: "old", ExpiresAt: time.Now().Add(-time.Second)})
			client, commands, stop := replySignalFixture(t, proof, ack, true)
			defer stop()
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			got := replySignalExchange(context.Background(), client, attempt)
			if got.ReplySignal == nil || got.ReplySignal.NativeTarget != tc.native || got.ReplySignal.Note != "old" || got.Completion.Failure.Code != "" {
				t.Fatalf("ack=%+v completion=%+v", got.ReplySignal, got.Completion)
			}
			select {
			case command := <-commands:
				if !strings.Contains(command, `"action":"prepare"`) || !strings.Contains(command, `"id":"`+parent+`"`) {
					t.Fatalf("native command=%q", command)
				}
			case <-time.After(time.Second):
				t.Fatal("fixture did not observe prepare")
			}
		})
	}
}

func TestReplySignalForeignParentDoesNotWrite(t *testing.T) {
	parent := strings.Repeat("b", 32)
	request := core.Request{Version: 1, RequestID: "signal-foreign", Operation: core.ReplySignalSet, Target: &core.Target{Kind: "direct", Name: "peer"}, ParentID: parent, ReplySignal: &core.ReplySignalInput{TTLSeconds: 10}}
	message := irc.MessageMetadata{ID: parent, Kind: "text", Target: "third", From: "other", Message: "private", Timestamp: time.Now().UTC()}
	proof := replySignalLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + replySignalLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	client, commands, stop := replySignalFixture(t, proof, "", true)
	defer stop()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := replySignalExchange(context.Background(), client, attempt)
	if got.Completion.Failure.Code != core.Forbidden || got.Completion.MayHavePosted || got.ReplySignal != nil {
		t.Fatalf("foreign outcome=%+v", got.Completion)
	}
	select {
	case command := <-commands:
		t.Fatalf("foreign parent reached write: %q", command)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReplySignalMalformedACKIsUncertainAndLookupDoesNotWrite(t *testing.T) {
	parent := strings.Repeat("c", 32)
	request := core.Request{Version: 1, RequestID: "signal-bad", Operation: core.ReplySignalSet, Target: &core.Target{Kind: "room", Name: "#room"}, ParentID: parent, ReplySignal: &core.ReplySignalInput{TTLSeconds: 10}}
	message := irc.MessageMetadata{ID: parent, Kind: "text", Target: "#room", From: "author", Message: "question", Timestamp: time.Now().UTC()}
	proof := replySignalLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + replySignalLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	bad := replySignalLine(t, irc.ChatEntry{Action: "prepare", Target: "#other", ID: parent, From: "reader", AccountID: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Second)})
	client, _, stop := replySignalFixture(t, proof, bad, true)
	defer stop()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := replySignalExchange(context.Background(), client, attempt)
	if got.Completion.Failure.Code != core.DeliveryUncertain || !got.Completion.MayHavePosted {
		t.Fatalf("bad ACK outcome=%+v", got.Completion)
	}
	attempt.Kind = store.LookupAttempt
	if got := replySignalExchange(context.Background(), nil, attempt); got.Completion.Failure.Code != core.Forbidden || got.Completion.MayHavePosted {
		t.Fatalf("lookup outcome=%+v", got.Completion)
	}
}

func TestReplySignalCancelPreservesNativeExpiry(t *testing.T) {
	parent := strings.Repeat("d", 32)
	request := core.Request{Version: 1, RequestID: "signal-cancel", Operation: core.ReplySignalCancel, Target: &core.Target{Kind: "room", Name: "#room"}, ParentID: parent}
	message := irc.MessageMetadata{ID: parent, Kind: "text", Target: "#room", From: "author", Message: "question", Timestamp: time.Now().UTC()}
	proof := replySignalLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + replySignalLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	nativeExpiry := time.Now().UTC()
	ack := replySignalLine(t, irc.ChatEntry{Action: "cancel", Target: "#room", ID: parent, From: "reader", ExpiresAt: nativeExpiry})
	client, _, stop := replySignalFixture(t, proof, ack, true)
	defer stop()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := replySignalExchange(context.Background(), client, attempt)
	if got.ReplySignal == nil || got.ReplySignal.Action != "cancel" || got.ReplySignal.AccountID != "" || !got.ReplySignal.ExpiresAt.Equal(nativeExpiry) {
		t.Fatalf("cancel acknowledgement=%+v completion=%+v", got.ReplySignal, got.Completion)
	}
}

func replySignalLine(t *testing.T, entry irc.ChatEntry) string {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(":server 777 reader :%s\r\n", data)
}

func replySignalFixture(t *testing.T, proof, ack string, terminator bool) (*irc.Client, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	commands := make(chan string, 1)
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprint(conn, ":server 005 reader CHAT=1 CONTEXT=1 CONTEXT_BYTES=1 :features\r\n:server 001 reader :welcome\r\n")
			}
			if strings.Contains(line, `"action":"context"`) {
				_, _ = fmt.Fprint(conn, proof, ":server 778 reader :End of chat response\r\n")
			} else if strings.Contains(line, `"action":"prepare"`) || strings.Contains(line, `"action":"cancel"`) {
				commands <- line
				_, _ = fmt.Fprint(conn, ack)
				if terminator {
					_, _ = fmt.Fprint(conn, ":server 778 reader :End of chat response\r\n")
				}
				return
			}
		}
	}()
	client, err := irc.DialContext(context.Background(), irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	return client, commands, func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("reply signal fixture did not stop")
		}
	}
}
