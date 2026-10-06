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

func TestPinMutationCollectorProvesBeforeWrite(t *testing.T) {
	parent := strings.Repeat("a", 32)
	target := &core.Target{Kind: "room", Name: "#room"}
	request := core.Request{Version: 1, RequestID: "pin-add", Operation: core.PinsAdd, Target: target, ParentID: parent}
	message := irc.MessageMetadata{ID: parent, Kind: "text", Target: target.Name, From: "author", Message: "root", Timestamp: time.Now().UTC()}
	contextLines := pinChatLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + pinChatLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	validPin := pinChatLine(t, irc.ChatEntry{Action: "pin", Target: target.Name, ID: parent})
	client, commands, stop := pinMutationFixture(t, contextLines, validPin)
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := pinExchange(ctx, client, attempt)
	if got.Pin == nil || got.Pin.ParentID != parent || got.Pin.Target != *target || got.Completion.Failure.Code != "" {
		t.Fatalf("pin acknowledgement=%+v completion=%+v", got.Pin, got.Completion)
	}
	select {
	case command := <-commands:
		if !strings.Contains(command, `"action":"pin"`) || !strings.Contains(command, `"id":"`+parent+`"`) {
			t.Fatalf("native pin command=%q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe pin command")
	}
}

func TestPinMutationRejectsForeignProofBeforeWrite(t *testing.T) {
	parent := strings.Repeat("b", 32)
	target := &core.Target{Kind: "room", Name: "#room"}
	request := core.Request{Version: 1, RequestID: "pin-foreign", Operation: core.PinsAdd, Target: target, ParentID: parent}
	foreign := irc.MessageMetadata{ID: parent, Kind: "text", Target: "#other", From: "author", Message: "root", Timestamp: time.Now().UTC()}
	proof := pinChatLine(t, irc.ChatEntry{Action: "context-message", Message: &foreign}) + pinChatLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	client, commands, stop := pinMutationFixture(t, proof, "")
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := pinExchange(ctx, client, attempt)
	if got.Completion.Failure.Code != core.Forbidden || got.Completion.MayHavePosted || got.Pin != nil {
		t.Fatalf("foreign proof outcome=%+v", got.Completion)
	}
	if err := client.Raw("PING :pin-no-write-fence"); err != nil {
		t.Fatal(err)
	}
	select {
	case command := <-commands:
		if command != "PING :pin-no-write-fence" {
			t.Fatalf("foreign proof reached native write: %q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("pin fixture did not reach ordered no-write fence")
	}
}

func TestPinMutationMarksPostWriteACKFailuresUncertain(t *testing.T) {
	parent := strings.Repeat("c", 32)
	target := &core.Target{Kind: "room", Name: "#room"}
	request := core.Request{Version: 1, RequestID: "pin-bad-ack", Operation: core.PinsAdd, Target: target, ParentID: parent}
	message := irc.MessageMetadata{ID: parent, Kind: "text", Target: target.Name, From: "author", Message: "root", Timestamp: time.Now().UTC()}
	proof := pinChatLine(t, irc.ChatEntry{Action: "context-message", Message: &message}) + pinChatLine(t, irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}})
	badAck := pinChatLine(t, irc.ChatEntry{Action: "pin", Target: "#other", ID: parent})
	client, _, stop := pinMutationFixture(t, proof, badAck)
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := pinExchange(ctx, client, attempt)
	if got.Completion.Failure.Code != core.DeliveryUncertain || !got.Completion.MayHavePosted || got.Pin != nil {
		t.Fatalf("bad ack outcome=%+v", got.Completion)
	}
}

func TestPinMutationRemovesRetainedPinFromSnapshot(t *testing.T) {
	parent := strings.Repeat("d", 32)
	target := &core.Target{Kind: "room", Name: "#room"}
	request := core.Request{Version: 1, RequestID: "pin-remove", Operation: core.PinsRemove, Target: target, ParentID: parent}
	message := irc.MessageMetadata{ID: parent, Kind: "text", Target: target.Name, From: "author", Message: "root", Timestamp: time.Now().UTC()}
	snapshot := pinChatLine(t, irc.ChatEntry{Action: "pin", Target: target.Name, ID: parent, From: message.From, Message: &message})
	ack := pinChatLine(t, irc.ChatEntry{Action: "unpin", Target: target.Name, ID: parent})
	client, commands, stop := pinMutationFixture(t, "", ack, snapshot)
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := pinExchange(ctx, client, attempt)
	if got.Pin == nil || got.Pin.Operation != core.PinsRemove || got.Completion.Failure.Code != "" {
		t.Fatalf("pin removal acknowledgement=%+v completion=%+v", got.Pin, got.Completion)
	}
	select {
	case command := <-commands:
		if !strings.Contains(command, `"action":"unpin"`) {
			t.Fatalf("native unpin command=%q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe unpin command")
	}
}

func pinChatLine(t *testing.T, entry irc.ChatEntry) string {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(":server 777 reader :%s\r\n", data)
}

func pinMutationFixture(t *testing.T, proof, ack string, snapshots ...string) (*irc.Client, <-chan string, func()) {
	return pinMutationScriptFixture(t, func(conn net.Conn, line string) bool {
		if strings.Contains(line, `"action":"context"`) {
			_, _ = fmt.Fprint(conn, proof, ":server 778 reader :End of chat response\r\n")
		} else if strings.Contains(line, `"action":"pins"`) {
			if len(snapshots) > 0 {
				_, _ = fmt.Fprint(conn, snapshots[0])
			}
			_, _ = fmt.Fprint(conn, ":server 778 reader :End of chat response\r\n")
		} else if strings.Contains(line, `"action":"pin"`) || strings.Contains(line, `"action":"unpin"`) {
			_, _ = fmt.Fprint(conn, ack, ":server 778 reader :End of chat response\r\n")
			return true
		}
		return false
	})
}

func pinMutationScriptFixture(t *testing.T, respond func(net.Conn, string) bool) (*irc.Client, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	commands := make(chan string, 2)
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
			if line == "PING :pin-no-write-fence" {
				commands <- line
				return
			}
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprint(conn, ":server 005 reader CHAT=1 CONTEXT=1 CONTEXT_BYTES=1 :features\r\n:server 001 reader :welcome\r\n")
			}
			if strings.Contains(line, `"action":"pin"`) || strings.Contains(line, `"action":"unpin"`) {
				commands <- line
			}
			if respond(conn, line) {
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
			t.Error("pin mutation fixture did not stop")
		}
	}
}
