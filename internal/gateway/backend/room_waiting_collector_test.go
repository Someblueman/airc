package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func chatLine(t *testing.T, entry irc.ChatEntry) string {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return dataToLine(data)
}

func TestBoundedRoomCollector(t *testing.T) {
	target := "#room"
	entry := irc.ChatEntry{Action: "room", Target: target, SlowSeconds: 12, HistoryLimit: 80}
	line := chatLine(t, entry)
	zeroLine := chatLine(t, irc.ChatEntry{Action: "room", Target: target})
	cases := []struct {
		name       string
		lines      []string
		continuous bool
		maxBytes   int
		wantErr    error
		wantSlow   int64
		wantLimit  int
	}{
		{name: "valid", lines: []string{line}, wantSlow: 12, wantLimit: 80},
		{name: "zero settings", lines: []string{zeroLine}, wantSlow: 0, wantLimit: 0},
		{name: "exact budget", lines: []string{line}, maxBytes: wireBudget([]string{line}), wantSlow: 12, wantLimit: 80},
		{name: "one below", lines: []string{line}, maxBytes: wireBudget([]string{line}) - 1, wantErr: store.ErrCapacity},
		{name: "empty response", wantErr: ErrReadUnavailable},
		{name: "duplicate entry", lines: []string{line, line}, wantErr: ErrReadUnavailable},
		{name: "mixed fields", lines: []string{chatLine(t, irc.ChatEntry{Action: "room", Target: target, Text: "bad"})}, wantErr: ErrReadUnavailable},
		{name: "foreign", lines: []string{chatLine(t, irc.ChatEntry{Action: "room", Target: "#other"})}, wantErr: ErrReadUnavailable},
		{name: "malformed", lines: []string{":server 777 reader :{bad-json\r\n"}, wantErr: ErrReadUnavailable},
		{name: "deadline under noise", lines: []string{line}, continuous: true, wantErr: ErrReadUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := contextFixture(t, tc.lines, tc.continuous)
			defer stop()
			timeout := 3 * time.Second
			if tc.continuous {
				timeout = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			max := tc.maxBytes
			if max == 0 {
				max = core.MaxRoomBytes
			}
			got, err := boundedRoomRequest(ctx, client, target, max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got != (core.RoomResult{}) {
					t.Fatalf("result=%+v error=%v, want no result and %v", got, err, tc.wantErr)
				}
				if tc.continuous && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatalf("context error = %v, want deadline", ctx.Err())
				}
				return
			}
			if err != nil || got.Target.Name != target || got.SlowSeconds != tc.wantSlow || got.HistoryLimit != tc.wantLimit {
				t.Fatalf("result=%+v error=%v", got, err)
			}
		})
	}
}

func TestBoundedWaitingCollector(t *testing.T) {
	target := "#room"
	parent := strings.Repeat("a", 32)
	valid := irc.ChatEntry{Action: "prepare", Target: target, ID: parent, From: "agent", Text: "thinking", ExpiresAt: time.Now().Add(time.Minute)}
	validLine := chatLine(t, valid)
	expired := valid
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	namespaceAccount := valid
	namespaceAccount.AccountID = strings.Repeat("d", 32)
	namespaceAccount.From = "account-agent"
	namespaceGuest := valid
	namespaceGuest.From = "guest"
	var overcount []string
	for i := 0; i < core.MaxWaitingSignals+1; i++ {
		m := valid
		m.ID = parent
		m.From = fmt.Sprintf("agent%04d", i)
		overcount = append(overcount, chatLine(t, m))
	}
	cases := []struct {
		name       string
		lines      []string
		continuous bool
		maxBytes   int
		wantErr    error
		wantCount  int
	}{
		{name: "empty", wantCount: 0},
		{name: "expiry fact preserved", lines: []string{chatLine(t, expired)}, wantCount: 1},
		{name: "valid", lines: []string{validLine}, wantCount: 1},
		{name: "exact budget", lines: []string{validLine}, maxBytes: wireBudget([]string{validLine}), wantCount: 1},
		{name: "one below", lines: []string{validLine}, maxBytes: wireBudget([]string{validLine}) - 1, wantErr: store.ErrCapacity},
		{name: "foreign", lines: []string{chatLine(t, irc.ChatEntry{Action: "prepare", Target: "#other", ID: parent, From: "agent", Text: "x", ExpiresAt: valid.ExpiresAt})}, wantErr: ErrReadUnavailable},
		{name: "mixed fields", lines: []string{chatLine(t, irc.ChatEntry{Action: "prepare", Target: target, ID: parent, From: "agent", Text: "x", SlowSeconds: 1, ExpiresAt: valid.ExpiresAt})}, wantErr: ErrReadUnavailable},
		{name: "identity mismatch", lines: []string{chatLine(t, irc.ChatEntry{Action: "prepare", Target: target, ID: strings.Repeat("c", 32), From: "agent", Text: "x", ExpiresAt: valid.ExpiresAt})}, wantErr: ErrReadUnavailable},
		{name: "duplicate actor", lines: []string{validLine, validLine}, wantErr: ErrReadUnavailable},
		{name: "account and guest namespaces", lines: []string{chatLine(t, namespaceAccount), chatLine(t, namespaceGuest)}, wantCount: 2},
		{name: "count overflow", lines: overcount, maxBytes: 1 << 20, wantErr: store.ErrCapacity},
		{name: "malformed", lines: []string{":server 777 reader :{bad-json\r\n"}, wantErr: ErrReadUnavailable},
		{name: "deadline under noise", lines: []string{validLine}, continuous: true, wantErr: ErrReadUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := contextFixture(t, tc.lines, tc.continuous)
			defer stop()
			timeout := 3 * time.Second
			if tc.continuous {
				timeout = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			max := tc.maxBytes
			if max == 0 {
				max = core.MaxWaitingBytes
			}
			got, err := boundedWaitingRequest(ctx, client, target, parent, max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got != nil {
					t.Fatalf("result=%v error=%v, want no result and %v", got, err, tc.wantErr)
				}
				if tc.continuous && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatalf("context error = %v, want deadline", ctx.Err())
				}
				return
			}
			if err != nil || len(got) != tc.wantCount || (tc.wantCount == 0 && got == nil) {
				t.Fatalf("result=%v error=%v", got, err)
			}
		})
	}
}
