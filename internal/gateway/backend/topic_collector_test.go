package backend

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestBoundedTopicCollector(t *testing.T) {
	text := ":server 332 reader #room :whole café ☃\r\n"
	meta := ":server 333 reader #room Author 1234\r\n"
	empty := ":server 331 reader #room :No topic is set\r\n"
	target := core.Target{Kind: "room", Name: "#room"}
	full := core.TopicResult{Target: target, Topic: "whole café ☃", SetBy: "Author", SetAt: time.Unix(1234, 0).UTC()}
	cases := []struct {
		name       string
		lines      []string
		maxBytes   int
		continuous bool
		want       core.TopicResult
		wantErr    error
	}{
		{name: "whole metadata", lines: []string{text, meta}, want: full},
		{name: "maximum header", lines: []string{":server 332 reader #room :" + strings.Repeat("a", 400) + "\r\n", meta}, want: core.TopicResult{Target: target, Topic: strings.Repeat("a", 400), SetBy: "Author", SetAt: full.SetAt}},
		{name: "Unix epoch timestamp", lines: []string{text, ":server 333 reader #room Author 0\r\n"}, want: core.TopicResult{Target: target, Topic: full.Topic, SetBy: "Author", SetAt: time.Unix(0, 0).UTC()}},
		{name: "empty topic", lines: []string{empty}, want: core.TopicResult{Target: target}},
		{name: "exact budget", lines: []string{text, meta}, maxBytes: len(text) + len(meta), want: full},
		{name: "one byte below", lines: []string{text, meta}, maxBytes: len(text) + len(meta) - 1, wantErr: store.ErrCapacity},
		{name: "empty budget overflow", lines: []string{empty}, maxBytes: len(empty) - 1, wantErr: store.ErrCapacity},
		{name: "missing result", wantErr: ErrReadUnavailable},
		{name: "missing metadata", lines: []string{text}, wantErr: ErrReadUnavailable},
		{name: "metadata first", lines: []string{meta, text}, wantErr: ErrReadUnavailable},
		{name: "duplicate text", lines: []string{text, text, meta}, wantErr: ErrReadUnavailable},
		{name: "foreign topic", lines: []string{strings.Replace(text, "#room", "#foreign", 1), meta}, wantErr: ErrReadUnavailable},
		{name: "foreign metadata", lines: []string{text, strings.Replace(meta, "#room", "#foreign", 1)}, wantErr: ErrReadUnavailable},
		{name: "foreign recipient", lines: []string{text, strings.Replace(meta, "reader", "another", 1)}, wantErr: ErrReadUnavailable},
		{name: "live event", lines: []string{":Author!user@host TOPIC #room :live topic\r\n", meta}, wantErr: ErrReadUnavailable},
		{name: "overlong topic", lines: []string{":server 332 reader #room :" + strings.Repeat("a", 401) + "\r\n", meta}, wantErr: ErrReadUnavailable},
		{name: "malformed setter", lines: []string{text, ":server 333 reader #room bad:nick 1234\r\n"}, wantErr: ErrReadUnavailable},
		{name: "timestamp overflow", lines: []string{text, ":server 333 reader #room Author 9223372036854775808\r\n"}, wantErr: ErrReadUnavailable},
		{name: "unrepresentable timestamp", lines: []string{text, ":server 333 reader #room Author 9223372036854775807\r\n"}, wantErr: ErrReadUnavailable},
		{name: "noncanonical time", lines: []string{text, ":server 333 reader #room Author +1234\r\n"}, wantErr: ErrReadUnavailable},
		{name: "extra metadata", lines: []string{text, ":server 333 reader #room Author 1234 extra\r\n"}, wantErr: ErrReadUnavailable},
		{name: "trailing metadata", lines: []string{text, ":server 333 reader #room Author 1234 :extra\r\n"}, wantErr: ErrReadUnavailable},
		{name: "upstream rejection", lines: []string{":server 403 reader #room :No such channel\r\n"}, wantErr: ErrReadUnavailable},
		{name: "deadline under noise", lines: []string{text}, continuous: true, wantErr: ErrReadUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := topicFixture(t, tc.lines, tc.continuous)
			defer stop()
			timeout := 3 * time.Second
			if tc.continuous {
				timeout = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			max := tc.maxBytes
			if max == 0 {
				max = core.MaxTopicBytes
			}
			got, err := boundedTopicRequest(ctx, client, target.Name, max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got != (core.TopicResult{}) {
					t.Fatalf("result=%+v err=%v want no result and %v", got, err, tc.wantErr)
				}
				if tc.continuous && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatalf("context error=%v", ctx.Err())
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("result=%+v err=%v want %+v", got, err, tc.want)
			}
		})
	}
}
