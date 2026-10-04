package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func contextFixture(n, bodySize int) (*Server, string) {
	s := New(Config{HistoryLimit: n + 1, OutboundBytes: 16 << 20})
	root := fmt.Sprintf("%032x", 1)
	for i := range n {
		m := Message{ID: fmt.Sprintf("%032x", i+1), From: "writer", Target: "#room", Body: strings.Repeat("x", bodySize), Seq: uint64(i + 1)}
		if i > 0 {
			m.ReplyTo, m.ThreadID = root, root
		}
		s.history.add(m)
	}
	return s, root
}

func contextResponse(t testing.TB, s *Server, id string, limit, budget int) ([]protocol.ChatEntry, string) {
	t.Helper()
	actor := &session{server: s, client: Client{Nick: "reader"}, out: make(chan string, 1256), done: make(chan struct{})}
	data, _ := json.Marshal(protocol.ChatRequest{Action: "context", ID: id, Limit: limit, MaxBytes: budget})
	s.chatLocked(actor, protocol.Command{Trailing: string(data)})
	var entries []protocol.ChatEntry
	var wire strings.Builder
	for len(actor.out) > 0 {
		line := <-actor.out
		wire.WriteString(line)
		command, err := protocol.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		if command.Name == "777" {
			var entry protocol.ChatEntry
			if err := json.Unmarshal([]byte(command.Trailing), &entry); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, entry)
		}
	}
	return entries, wire.String()
}

func TestContextWireBudget(t *testing.T) {
	s, root := contextFixture(12, 80)
	trigger := s.history.at(1).ID
	correction := s.history.at(2)
	correction.ID = fmt.Sprintf("%032x", 13)
	correction.Body = "corrected é🐈\n\"<>&"
	correction.Kind, correction.Supersedes = "correct", trigger
	s.history.add(correction)
	s.chat.Pins["#room"] = []Message{s.history.at(0)}
	_, full := contextResponse(t, s, trigger, 1000, 1<<20)
	for _, budget := range []int{1024, 1500, len(full) - 1, len(full), len(full) + 1} {
		entries, wire := contextResponse(t, s, trigger, 1000, budget)
		if strings.Contains(wire, " 461 ") {
			if len(entries) != 0 || strings.Count(wire, "\r\n") != 1 {
				t.Fatal("protected budget failure sent partial context")
			}
			continue
		}
		if len(wire) > budget || !strings.HasSuffix(wire, ":End of chat response\r\n") {
			t.Fatalf("response bytes %d exceed %d or terminator missing", len(wire), budget)
		}
		kept := map[string]bool{}
		messages, pins, profiles := 0, 0, 0
		var summary *protocol.ContextSummary
		for _, e := range entries {
			switch e.Action {
			case "context-message":
				messages++
				kept[e.Message.ID] = true
				original, _ := s.history.message(e.Message.ID)
				if e.Message.Message != original.Body {
					t.Fatal("body changed")
				}
			case "context-pin":
				pins++
			case "context-profile":
				profiles++
			case "context":
				summary = e.Context
			}
		}
		if !kept[root] || !kept[trigger] || !kept[correction.ID] || summary == nil || summary.OmittedMessages != 13-messages || summary.OmittedPins != 1-pins || summary.OmittedProfiles != 1-profiles {
			t.Fatal("protected records or omission counts incorrect", wire)
		}
		if budget >= len(full) && wire != full {
			t.Fatal("exact-fit budget changed complete response")
		}
	}
}

func TestContextBudgetRejectsBeforeSendingAndBoundsLargeRetrieval(t *testing.T) {
	s, root := contextFixture(1000, 3500)
	for _, budget := range []int{-1, 1, 1024, (1 << 20) + 1} {
		entries, wire := contextResponse(t, s, root, 1000, budget)
		if len(entries) != 0 || !strings.Contains(wire, " 461 ") || strings.Count(wire, "\r\n") != 1 {
			t.Fatal("expected one rejection, no partial context", wire)
		}
	}
	entries, wire := contextResponse(t, s, root, 1000, 32768)
	summary := entries[len(entries)-1].Context
	if len(wire) > 32768 || summary == nil || summary.OmittedMessages < 990 || entries[0].Message.ID != root {
		t.Fatal("large retrieval not bounded", len(wire), summary)
	}
	t.Logf("1000 x 3500-byte messages: %d response bytes; %d messages omitted", len(wire), summary.OmittedMessages)
}

func BenchmarkContextWireBudget(b *testing.B) {
	s, root := contextFixture(1000, 3500)
	for _, budget := range []int{0, 32768} {
		b.Run(fmt.Sprint(budget), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				contextResponse(b, s, root, 1000, budget)
			}
		})
	}
}
