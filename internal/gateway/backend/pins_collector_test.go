package backend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBoundedPinsCollector(t *testing.T) {
	target := "#allowed"
	message := irc.MessageMetadata{ID: strings.Repeat("a", 32), Kind: "text", Target: target, From: "author", Message: "pinned", Timestamp: time.Now().UTC()}
	largeMessage := message
	largeMessage.ID = strings.Repeat("b", 32)
	largeMessage.Message = strings.Repeat("x", 4096)
	entry := func(m irc.MessageMetadata, room string) string {
		data, err := json.Marshal(irc.ChatEntry{Action: "pin", Target: room, ID: m.ID, From: m.From, Message: &m})
		if err != nil {
			t.Fatal(err)
		}
		return dataToLine(data)
	}
	cases := []struct {
		name         string
		lines        []string
		continuous   bool
		maxBytes     int
		wantErr      error
		wantCount    int
		wantDeadline bool
	}{
		{name: "empty complete", wantCount: 0},
		{name: "valid complete", lines: []string{entry(message, target)}, wantCount: 1},
		{name: "malformed 777", lines: []string{":server 777 reader :{bad-json\r\n"}, wantErr: ErrReadUnavailable},
		{name: "unexpected action", lines: []string{func() string {
			data, _ := json.Marshal(irc.ChatEntry{Action: "context", Target: target})
			return dataToLine(data)
		}()}, wantErr: ErrReadUnavailable},
		{name: "foreign target", lines: []string{entry(message, "#foreign")}, wantErr: ErrReadUnavailable},
		{name: "duplicate", lines: []string{entry(message, target), entry(message, target)}, wantErr: ErrReadUnavailable},
		{name: "embedded target mismatch", lines: []string{func() string { bad := message; bad.Target = "#foreign"; return entry(bad, target) }()}, wantErr: ErrReadUnavailable},
		{name: "embedded identity mismatch", lines: []string{func() string {
			data, _ := json.Marshal(irc.ChatEntry{Action: "pin", Target: target, ID: "other", From: message.From, Message: &message})
			return dataToLine(data)
		}()}, wantErr: ErrReadUnavailable},
		{name: "mixed pin payload", lines: []string{func() string {
			data, _ := json.Marshal(irc.ChatEntry{Action: "pin", Target: target, ID: message.ID, From: message.From, Text: "unexpected", Message: &message})
			return dataToLine(data)
		}()}, wantErr: ErrReadUnavailable},
		{name: "wire overflow", lines: []string{entry(largeMessage, target)}, maxBytes: 1024, wantErr: store.ErrCapacity},
		{name: "cancellation", lines: []string{entry(message, target)}, continuous: true, wantErr: ErrReadUnavailable, wantDeadline: true},
	}
	validLine := entry(message, target)
	cases = append(cases,
		struct {
			name         string
			lines        []string
			continuous   bool
			maxBytes     int
			wantErr      error
			wantCount    int
			wantDeadline bool
		}{name: "exact wire budget", lines: []string{validLine}, maxBytes: wireBudget([]string{validLine}), wantCount: 1},
		struct {
			name         string
			lines        []string
			continuous   bool
			maxBytes     int
			wantErr      error
			wantCount    int
			wantDeadline bool
		}{name: "one byte below wire budget", lines: []string{validLine}, maxBytes: wireBudget([]string{validLine}) - 1, wantErr: store.ErrCapacity},
	)
	var six []string
	for i := 0; i < core.MaxPins+1; i++ {
		m := message
		m.ID = strings.Repeat(string(rune('a'+i)), 32)
		six = append(six, entry(m, target))
	}
	cases = append(cases, struct {
		name         string
		lines        []string
		continuous   bool
		maxBytes     int
		wantErr      error
		wantCount    int
		wantDeadline bool
	}{name: "six distinct pins", lines: six, wantErr: store.ErrCapacity})
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
				maxBytes = core.MaxPinsBytes
			}
			pins, err := boundedPinsRequestBudget(ctx, client, target, maxBytes)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if pins != nil {
					t.Fatalf("failed collection returned %d pins", len(pins))
				}
				if tc.wantDeadline && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
				}
				return
			}
			if err != nil || len(pins) != tc.wantCount {
				t.Fatalf("pins=%d error=%v, want count %d", len(pins), err, tc.wantCount)
			}
			if tc.wantCount == 0 && pins == nil {
				t.Fatal("empty collection returned nil pins")
			}
		})
	}
}
