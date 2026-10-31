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

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestActivityCollectorAcceptsNativeThrottledKind(t *testing.T) {
	target := &core.Target{Kind: "room", Name: "#room"}
	request := core.Request{Version: 1, RequestID: "activity", Operation: core.ActivitySet, Target: target, Activity: &core.ActivityInput{Kind: "typing", Note: "new", TTLSeconds: 10}}
	entry := irc.ChatEntry{Action: "thinking", Target: target.Name, From: "reader", AccountID: strings.Repeat("a", 32), Text: "old", ExpiresAt: time.Now().Add(-time.Second)}
	line := activityChatLine(t, entry)
	client, commands, stop := activityFixture(t, []string{line}, true, false)
	defer stop()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
	got := activityExchange(context.Background(), client, attempt)
	if got.Activity == nil || got.Activity.Kind != "thinking" || got.Activity.Note != "old" || got.Activity.Target != *target || got.Completion.Failure.Code != "" {
		t.Fatalf("activity acknowledgement=%+v completion=%+v", got.Activity, got.Completion)
	}
	select {
	case command := <-commands:
		if !strings.Contains(command, `"action":"typing"`) || !strings.Contains(command, `"target":"#room"`) {
			t.Fatalf("native activity command=%q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe activity command")
	}
}

func TestActivityCollectorRejectsMalformedAfterWrite(t *testing.T) {
	target := &core.Target{Kind: "room", Name: "#room"}
	request := core.Request{Version: 1, RequestID: "activity-bad", Operation: core.ActivitySet, Target: target, Activity: &core.ActivityInput{Kind: "thinking", TTLSeconds: 5}}
	cases := []struct {
		name  string
		lines []string
	}{
		{"foreign", []string{activityChatLine(t, irc.ChatEntry{Action: "thinking", Target: "#other", From: "reader", AccountID: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Second)})}},
		{"mixed", []string{activityChatLine(t, irc.ChatEntry{Action: "thinking", Target: "#room", From: "reader", AccountID: strings.Repeat("a", 32), ID: "unexpected", ExpiresAt: time.Now().Add(time.Second)})}},
		{"settings", []string{activityChatLine(t, irc.ChatEntry{Action: "thinking", Target: "#room", From: "reader", AccountID: strings.Repeat("a", 32), SlowSeconds: 1, ExpiresAt: time.Now().Add(time.Second)})}},
		{"duplicate", []string{activityChatLine(t, irc.ChatEntry{Action: "thinking", Target: "#room", From: "reader", AccountID: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Second)}), activityChatLine(t, irc.ChatEntry{Action: "thinking", Target: "#room", From: "reader", AccountID: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Second)})}},
		{"raw", []string{":server 777 reader :{bad-json\r\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _, stop := activityFixture(t, tc.lines, true, false)
			defer stop()
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			got := activityExchange(context.Background(), client, attempt)
			if got.Completion.Failure.Code != core.DeliveryUncertain || !got.Completion.MayHavePosted || got.Activity != nil {
				t.Fatalf("outcome=%+v", got.Completion)
			}
		})
	}
}

func TestActivityCollectorDeadlineUnderNoiseAndLookupNeverWrites(t *testing.T) {
	target := &core.Target{Kind: "direct", Name: "reader"}
	request := core.Request{Version: 1, RequestID: "activity-timeout", Operation: core.ActivitySet, Target: target, Activity: &core.ActivityInput{Kind: "typing", TTLSeconds: 5}}
	client, commands, stop := activityFixture(t, nil, false, true)
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(time.Second), Job: store.Job{Request: request}}
	got := activityExchange(ctx, client, attempt)
	if got.Completion.Failure.Code != core.DeliveryUncertain || !got.Completion.MayHavePosted || got.Activity != nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("noise timeout outcome=%+v ctx=%v", got.Completion, ctx.Err())
	}
	select {
	case <-commands:
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe activity write")
	}
	lookup := attempt
	lookup.Kind = store.LookupAttempt
	lookup.Job.Request.Target = target
	if got := activityExchange(context.Background(), nil, lookup); got.Completion.Failure.Code != core.Forbidden || got.Completion.MayHavePosted {
		t.Fatalf("lookup outcome=%+v", got.Completion)
	}
}

func activityChatLine(t *testing.T, entry irc.ChatEntry) string {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(":server 777 reader :%s\r\n", data)
}

func activityFixture(t *testing.T, lines []string, terminator, noise bool) (*irc.Client, <-chan string, func()) {
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
				_, _ = fmt.Fprintf(conn, ":server 005 reader AIRC=%d :features\r\n:server 001 reader :welcome\r\n", protocol.Level)
			}
			if strings.Contains(line, `"action":"typing"`) || strings.Contains(line, `"action":"thinking"`) {
				commands <- line
				for _, response := range lines {
					_, _ = fmt.Fprint(conn, response)
				}
				if terminator {
					_, _ = fmt.Fprint(conn, ":server 778 reader :End of chat response\r\n")
				}
				if noise {
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for range ticker.C {
						_ = conn.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
						if _, err := fmt.Fprint(conn, ":server NOTICE reader :noise\r\n"); err != nil {
							return
						}
					}
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
			t.Error("activity fixture did not stop")
		}
	}
}
