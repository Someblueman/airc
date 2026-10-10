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
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestCardMutationCollector(t *testing.T) {
	model := "reasoner"
	request := core.Request{Version: 1, RequestID: "profile-1", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model}}
	card := irc.AgentCard{Nick: "reader", AccountID: strings.Repeat("a", 32), Model: model, State: "unknown", LastSeen: time.Now().UTC()}
	cases := []struct {
		name string
		card irc.AgentCard
		end  bool
		want core.ErrorCode
	}{
		{name: "valid", card: card, end: true},
		{name: "foreign card", card: irc.AgentCard{Nick: "other", Model: model, State: "unknown"}, end: true, want: core.DeliveryUncertain},
		{name: "duplicate card", card: card, end: true, want: core.DeliveryUncertain},
		{name: "missing terminator", card: card, want: core.DeliveryUncertain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{directoryLine(t, tc.card)}
			if tc.name == "duplicate card" {
				lines = append(lines, directoryLine(t, tc.card))
			}
			client, stop, commands := cardFixture(t, lines, tc.end)
			defer stop()
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(time.Second), Job: store.Job{Request: request}}
			got := cardExchange(ctx, client, attempt)
			if tc.want == "" && got.Card == nil {
				t.Fatalf("card exchange failed before command assertion: %+v", got.Completion)
			}
			if tc.want != "" && got.Completion.Failure.Code != tc.want {
				t.Fatalf("card exchange failure before command assertion: %+v want=%s", got.Completion, tc.want)
			}
			var command string
			select {
			case command = <-commands:
			case <-time.After(time.Second):
				t.Fatal("fixture did not observe card command")
			}
			if command != `PROFILE :{"model":"reasoner"}` {
				t.Fatalf("outgoing profile command = %q", command)
			}
			if tc.want == "" {
				if got.Card == nil || got.Card.Card != tc.card {
					t.Fatalf("card=%+v completion=%+v", got.Card, got.Completion)
				}
				return
			}
			if got.Completion.Failure.Code != tc.want || got.Card != nil {
				t.Fatalf("card=%+v completion=%+v want=%s", got.Card, got.Completion, tc.want)
			}
		})
	}
}

func TestPresenceMutationCollector(t *testing.T) {
	request := core.Request{Version: 1, RequestID: "presence-1", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "thinking", Note: "working", TTLSeconds: 60}}
	now := time.Now().UTC().Truncate(time.Second)
	card := irc.AgentCard{Nick: "reader", AccountID: strings.Repeat("a", 32), State: "thinking", Note: "working", ExpiresAt: now.Add(time.Minute), LastSeen: now}
	client, stop, commands := cardFixture(t, []string{directoryLine(t, card)}, true)
	defer stop()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(time.Second), Job: store.Job{Request: request}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got := cardExchange(ctx, client, attempt)
	if got.Card == nil {
		t.Fatalf("presence exchange failed before command assertion: %+v", got.Completion)
	}
	var command string
	select {
	case command = <-commands:
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe presence command")
	}
	if command != "PRESENCE thinking 60 :working" {
		t.Fatalf("outgoing presence command = %q", command)
	}
	if got.Card == nil || got.Card.Card.State != card.State || got.Card.Card.Note != card.Note || got.Completion.Failure.Code != "" {
		t.Fatalf("presence card=%+v completion=%+v", got.Card, got.Completion)
	}
}

func TestProfileMutationCollectorAcceptsEscapedAcknowledgementOverFourKiB(t *testing.T) {
	value := strings.Repeat("<", 200)
	patch := &core.ProfilePatch{Model: &value}
	request := core.Request{Version: 1, RequestID: "profile-large", Operation: core.ProfileUpdate, Profile: patch}
	now := time.Now().UTC()
	card := irc.AgentCard{Nick: "reader", AccountID: strings.Repeat("a", 32), Model: value, Workspace: value, Tools: value, About: value, State: "unknown", LastSeen: now}
	client, stop, commands := cardFixture(t, []string{directoryLine(t, card)}, true)
	defer stop()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(time.Second), Job: store.Job{Request: request}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	got := cardExchange(ctx, client, attempt)
	if got.Card == nil {
		t.Fatalf("large profile exchange failed before command assertion: %+v", got.Completion)
	}
	wantFields, _ := json.Marshal(map[string]string{"model": value})
	var command string
	select {
	case command = <-commands:
	case <-time.After(time.Second):
		t.Fatal("fixture did not observe large profile command")
	}
	if command != "PROFILE :"+string(wantFields) {
		t.Fatalf("outgoing large profile command was not exact: %q", command)
	}
	if got.Card == nil || got.Card.Card != card || got.Completion.Failure.Code != "" {
		t.Fatalf("large profile card=%+v completion=%+v", got.Card, got.Completion)
	}
}

func directoryLine(t *testing.T, card irc.AgentCard) string {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Format("server", "773", []string{"reader"}, string(data))
}

func cardFixture(t *testing.T, lines []string, end bool) (*irc.Client, func(), <-chan string) {
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
				_, _ = fmt.Fprint(conn, ":server 005 reader DIRECTORY=1 :features\r\n:server 001 reader :welcome\r\n")
			}
			if strings.HasPrefix(line, "PROFILE ") || strings.HasPrefix(line, "PRESENCE ") {
				commands <- line
				for _, response := range lines {
					_, _ = fmt.Fprint(conn, response)
				}
				if end {
					_, _ = fmt.Fprint(conn, ":server 774 reader :End of directory\r\n")
				}
				return
			}
		}
	}()
	client, err := irc.DialContext(context.Background(), irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	return client, func() { _ = client.Close(); _ = listener.Close(); <-done }, commands
}
