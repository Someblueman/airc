package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

func TestBoundedContextCollectorRejectsAdverseResponses(t *testing.T) {
	parent := strings.Repeat("a", 32)
	message := irc.MessageMetadata{ID: parent, Target: "#allowed", From: "author", Message: "root", Timestamp: time.Now().UTC()}
	summary := irc.ChatEntry{Action: "context", Context: &irc.ContextSummary{TriggerID: parent, RootID: parent}}
	messageEntry := irc.ChatEntry{Action: "context-message", Message: &message}
	profileEntry := irc.ChatEntry{Action: "context-profile", Profile: &irc.AgentCard{Nick: "author", State: "invalid"}}
	entryJSON := func(entry irc.ChatEntry) string {
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		return dataToLine(data)
	}
	cases := []struct {
		name       string
		lines      []string
		maxBytes   int
		continuous bool
		want       error
	}{
		{name: "malformed 777", lines: []string{":server 777 reader :{bad-json\r\n", entryJSON(messageEntry), entryJSON(summary)}, want: ErrReadUnavailable},
		{name: "mixed action payload", lines: []string{entryJSON(irc.ChatEntry{Action: "context-message", Context: &irc.ContextSummary{TriggerID: parent}, Message: &message}), entryJSON(summary)}, want: ErrReadUnavailable},
		{name: "duplicate summary", lines: []string{entryJSON(summary), entryJSON(summary)}, want: ErrReadUnavailable},
		{name: "post summary entry", lines: []string{entryJSON(summary), entryJSON(messageEntry)}, want: ErrReadUnavailable},
		{name: "invalid profile", lines: []string{entryJSON(profileEntry), entryJSON(summary)}, want: ErrReadUnavailable},
		{name: "wire overflow", lines: []string{entryJSON(irc.ChatEntry{Action: "context-message", Message: &irc.MessageMetadata{ID: parent, Target: "#allowed", From: "author", Message: strings.Repeat("x", 4096), Timestamp: time.Now().UTC()}})}, maxBytes: 1024, want: store.ErrCapacity},
		{name: "cancel with unrelated traffic", lines: []string{entryJSON(summary)}, continuous: true, want: ErrReadUnavailable},
	}
	positiveLines := []string{entryJSON(messageEntry), entryJSON(summary)}
	positiveBudget := wireBudget(positiveLines)
	cases = append(cases,
		struct {
			name       string
			lines      []string
			maxBytes   int
			continuous bool
			want       error
		}{name: "positive exact budget", lines: positiveLines, maxBytes: positiveBudget},
		struct {
			name       string
			lines      []string
			maxBytes   int
			continuous bool
			want       error
		}{name: "one byte below budget", lines: positiveLines, maxBytes: positiveBudget - 1, want: store.ErrCapacity},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := contextFixture(t, tc.lines, tc.continuous)
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if tc.continuous {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 60*time.Millisecond)
				defer cancel()
			}
			maxBytes := tc.maxBytes
			if maxBytes == 0 {
				maxBytes = core.MaxContextBytes
			}
			_, err := boundedContextRequest(ctx, client, parent, 50, maxBytes, "room", nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func contextFixture(t *testing.T, lines []string, continuous bool) (*irc.Client, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprintf(conn, ":server 005 reader CHAT=1 CONTEXT=1 CONTEXT_BYTES=1 :features\r\n:server 001 reader :welcome\r\n")
			}
			if !strings.HasPrefix(line, "CHAT :") {
				continue
			}
			for _, response := range lines {
				_, _ = fmt.Fprint(conn, response)
			}
			if continuous {
				for {
					if _, err := fmt.Fprint(conn, ":server NOTICE reader :noise\r\n"); err != nil {
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
			_, _ = fmt.Fprint(conn, ":server 778 reader :End of chat response\r\n")
			return
		}
	}()
	client, err := irc.DialContext(context.Background(), irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("context fixture did not stop after dial failure")
		}
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("context fixture did not stop")
		}
	}
}

func dataToLine(data []byte) string {
	return fmt.Sprintf(":server 777 reader :%s\r\n", data)
}

func wireBudget(lines []string) int {
	budget := len(protocol.Format("server", "778", []string{"reader"}, "End of chat response"))
	for _, line := range lines {
		budget += len(line)
	}
	return budget
}
