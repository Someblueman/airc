package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBoundedAgentsCollector(t *testing.T) {
	when := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	line := agentResponse(t, irc.AgentInfo{Nick: "Zed", Channels: []string{"#z", "#A"}, ConnectedAt: when})
	end := ":server 764 reader :End of agents list\r\n"
	tooMany := make([]string, 0, core.MaxAgents+1)
	for i := 0; i <= core.MaxAgents; i++ {
		tooMany = append(tooMany, agentResponse(t, irc.AgentInfo{Nick: fmt.Sprintf("agent%d", i), ConnectedAt: when}))
	}
	escaping := make([]string, 0, 7)
	for i := 0; i < 6; i++ {
		rooms := make([]string, 32)
		for j := range rooms {
			rooms[j] = fmt.Sprintf("#%02d%s", j, strings.Repeat("<", 60))
		}
		// Valid native JSON can use literal '<'; the emitted gateway JSON escapes
		// it, so this isolates output overflow below the native wire byte bound.
		record := agentResponse(t, irc.AgentInfo{Nick: fmt.Sprintf("escaping%d", i), Channels: rooms, ConnectedAt: when})
		escaping = append(escaping, strings.ReplaceAll(record, `\u003c`, "<"))
	}
	cases := []struct {
		name       string
		lines      []string
		max        int
		limit      int
		continuous bool
		want       int
		wantErr    error
	}{
		{name: "complete", lines: []string{line, end}, want: 1},
		{name: "empty", lines: []string{end}, want: 0},
		{name: "duplicate canonical nick", lines: []string{agentResponse(t, irc.AgentInfo{Nick: "Alice", ConnectedAt: when}), agentResponse(t, irc.AgentInfo{Nick: "alice", ConnectedAt: when}), end}, wantErr: ErrReadUnavailable},
		{name: "malformed JSON", lines: []string{":server 763 reader :{\"nick\":\"alice\"}\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "unknown JSON field", lines: []string{":server 763 reader :{\"nick\":\"alice\",\"channels\":[],\"connected_at\":\"2026-01-01T00:00:00Z\",\"extra\":1}\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "foreign recipient", lines: []string{strings.Replace(line, "reader", "other", 1), end}, wantErr: ErrReadUnavailable},
		{name: "null rooms", lines: []string{strings.Replace(line, `["#z","#A"]`, `null`, 1), end}, wantErr: ErrReadUnavailable},
		{name: "duplicate JSON key", lines: []string{strings.Replace(line, `"nick":"Zed"`, `"nick":"Zed","nick":"Zed"`, 1), end}, wantErr: ErrReadUnavailable},
		{name: "foreign terminator", lines: []string{line, strings.Replace(end, "reader", "other", 1)}, wantErr: ErrReadUnavailable},
		{name: "extra terminator parameter", lines: []string{line, strings.Replace(end, "reader", "reader extra", 1)}, wantErr: ErrReadUnavailable},
		{name: "wrong terminator label", lines: []string{line, strings.Replace(end, "End of agents list", "complete", 1)}, wantErr: ErrReadUnavailable},
		{name: "request count overflow", lines: []string{line, agentResponse(t, irc.AgentInfo{Nick: "Alice", ConnectedAt: when}), end}, limit: 1, wantErr: store.ErrCapacity},
		{name: "bad room", lines: []string{agentResponse(t, irc.AgentInfo{Nick: "alice", Channels: []string{"room"}, ConnectedAt: when}), end}, wantErr: ErrReadUnavailable},
		{name: "future timestamp preserved", lines: []string{agentResponse(t, irc.AgentInfo{Nick: "alice", ConnectedAt: time.Now().Add(time.Minute)}), end}, want: 1},
		{name: "count overflow", lines: append(tooMany, end), wantErr: store.ErrCapacity},
		{name: "wire overflow", lines: []string{line, end}, max: len(line) + len(end) - 1, wantErr: store.ErrCapacity},
		{name: "escaped output overflow", lines: append(escaping, end), wantErr: store.ErrCapacity},
		{name: "missing terminator", lines: []string{line}, wantErr: ErrReadUnavailable},
		{name: "cancel", lines: []string{line}, continuous: true, wantErr: ErrReadUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := agentsFixture(t, tc.lines, tc.continuous)
			defer stop()
			d := 2 * time.Second
			if tc.continuous {
				d = 60 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), d)
			defer cancel()
			max := tc.max
			if max == 0 {
				max = core.MaxAgentsBytes
			}
			limit := tc.limit
			if limit == 0 {
				limit = core.MaxAgents
			}
			got, err := boundedAgentsRequest(ctx, client, max, limit)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got.Agents != nil || got.Version != 0 {
					t.Fatalf("result=%+v err=%v want %v", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got.Version != 1 || len(got.Agents) != tc.want || got.Agents == nil {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if tc.name == "complete" && (got.Agents[0].Nick != "Zed" || strings.Join(got.Agents[0].Channels, ",") != "#A,#z") {
				t.Fatalf("metadata was not preserved/sorted: %+v", got.Agents[0])
			}
			for _, agent := range got.Agents {
				if agent.Channels == nil {
					t.Fatal("empty native rooms changed into null")
				}
			}
		})
	}
}
