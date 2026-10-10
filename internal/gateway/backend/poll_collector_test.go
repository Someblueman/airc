package backend

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBoundedPollCollector(t *testing.T) {
	target, id := "#room", strings.Repeat("a", 32)
	entry := irc.ChatEntry{Action: "results", Target: target, ID: id, From: "Author", AccountID: strings.Repeat("b", 32), Text: "Question café ☃?", Options: []string{"first", "second"}, Votes: []int{2, 1}, ExpiresAt: time.Now().UTC().Add(-time.Minute), Closed: true}
	line := chatLine(t, entry)
	want := core.PollResult{Target: core.Target{Kind: "room", Name: target}, ID: id, From: entry.From, AccountID: entry.AccountID, Question: entry.Text, Options: entry.Options, Votes: entry.Votes, ExpiresAt: entry.ExpiresAt, Closed: entry.Closed}
	changed := func(edit func(*irc.ChatEntry)) string {
		copy := entry
		copy.Options = append([]string(nil), entry.Options...)
		copy.Votes = append([]int(nil), entry.Votes...)
		edit(&copy)
		return chatLine(t, copy)
	}
	cases := []struct {
		name       string
		lines      []string
		maxBytes   int
		continuous bool
		wantErr    error
	}{
		{name: "whole expired native result", lines: []string{line}},
		{name: "exact wire budget", lines: []string{line}, maxBytes: wireBudget([]string{line})},
		{name: "one byte below", lines: []string{line}, maxBytes: wireBudget([]string{line}) - 1, wantErr: store.ErrCapacity},
		{name: "missing result", wantErr: ErrReadUnavailable},
		{name: "duplicate", lines: []string{line, line}, wantErr: ErrReadUnavailable},
		{name: "foreign room", lines: []string{changed(func(e *irc.ChatEntry) { e.Target = "#foreign" })}, wantErr: ErrReadUnavailable},
		{name: "wrong poll", lines: []string{changed(func(e *irc.ChatEntry) { e.ID = strings.Repeat("c", 32) })}, wantErr: ErrReadUnavailable},
		{name: "wrong action", lines: []string{changed(func(e *irc.ChatEntry) { e.Action = "prepare" })}, wantErr: ErrReadUnavailable},
		{name: "mixed fields", lines: []string{changed(func(e *irc.ChatEntry) { e.HistoryLimit = 1 })}, wantErr: ErrReadUnavailable},
		{name: "missing expiry", lines: []string{changed(func(e *irc.ChatEntry) { e.ExpiresAt = time.Time{} })}, wantErr: ErrReadUnavailable},
		{name: "bad author", lines: []string{changed(func(e *irc.ChatEntry) { e.From = "bad author" })}, wantErr: ErrReadUnavailable},
		{name: "bad account", lines: []string{changed(func(e *irc.ChatEntry) { e.AccountID = "arbitrary" })}, wantErr: ErrReadUnavailable},
		{name: "blank question", lines: []string{changed(func(e *irc.ChatEntry) { e.Text = " " })}, wantErr: ErrReadUnavailable},
		{name: "duplicate options", lines: []string{changed(func(e *irc.ChatEntry) { e.Options[1] = e.Options[0] })}, wantErr: ErrReadUnavailable},
		{name: "vote arity", lines: []string{changed(func(e *irc.ChatEntry) { e.Votes = nil })}, wantErr: ErrReadUnavailable},
		{name: "negative votes", lines: []string{changed(func(e *irc.ChatEntry) { e.Votes[0] = -1 })}, wantErr: ErrReadUnavailable},
		{name: "voter capacity", lines: []string{changed(func(e *irc.ChatEntry) { e.Votes = []int{256, 1} })}, wantErr: ErrReadUnavailable},
		{name: "malformed JSON", lines: []string{":server 777 reader :{bad-json\r\n"}, wantErr: ErrReadUnavailable},
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
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			max := tc.maxBytes
			if max == 0 {
				max = core.MaxPollBytes
			}
			got, err := boundedPollRequest(ctx, client, target, id, max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(got, core.PollResult{}) {
					t.Fatalf("result=%+v err=%v want no result and %v", got, err, tc.wantErr)
				}
				if tc.continuous && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatalf("context error=%v", ctx.Err())
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("result=%+v err=%v want %+v", got, err, want)
			}
		})
	}
}

func TestPollEntryPreservesGuestAndClockFacts(t *testing.T) {
	entry := irc.ChatEntry{Action: "results", Target: "#room", ID: strings.Repeat("a", 32), From: "guest", Text: "Question?", Options: []string{" ", "answer"}, Votes: []int{0, 256}, ExpiresAt: time.Now().Add(-time.Minute)}
	if !validPollEntry(entry, entry.Target, entry.ID) {
		t.Fatal("valid guest result with native Closed false was rejected")
	}
}
