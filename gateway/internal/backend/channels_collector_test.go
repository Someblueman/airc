package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
)

func TestBoundedChannelsCollector(t *testing.T) {
	channel := core.Channel{Name: "#room", Members: 2, Messages: 1, LastActivity: time.Unix(1234, 0).UTC(), Topic: "café"}
	payload, _ := json.Marshal(channel)
	entry := fmt.Sprintf(":server 768 reader :%s\r\n", payload)
	end := ":server 769 reader :End of channels list\r\n"
	maxLines := channelLines(256)
	overLines := channelLines(257)
	first, last := channel, channel
	first.Name, last.Name = "#a", "#z"
	firstJSON, _ := json.Marshal(first)
	lastJSON, _ := json.Marshal(last)
	upper := first
	upper.Name = "#A"
	upperJSON, _ := json.Marshal(upper)
	escaped := `:server 768 reader :{"name":"#escaped","members":1,"messages":0,"topic":"` + strings.Repeat("\u2028", 100) + "\"}\r\n"
	cases := []struct {
		name         string
		lines        []string
		max          int
		continuous   bool
		want         int
		wantErr      error
		wantChannels []core.Channel
	}{
		{name: "positive", lines: []string{entry, end}, want: 1, wantChannels: []core.Channel{channel}},
		{name: "sorted whole metadata", lines: []string{fmt.Sprintf(":server 768 reader :%s\r\n", lastJSON), fmt.Sprintf(":server 768 reader :%s\r\n", firstJSON), end}, want: 2, wantChannels: []core.Channel{first, last}},
		{name: "case distinct rooms", lines: []string{fmt.Sprintf(":server 768 reader :%s\r\n", firstJSON), fmt.Sprintf(":server 768 reader :%s\r\n", upperJSON), end}, want: 2, wantChannels: []core.Channel{upper, first}},
		{name: "empty", lines: []string{end}, want: 0},
		{name: "foreign recipient", lines: []string{strings.Replace(entry, "reader", "other", 1), end}, wantErr: ErrReadUnavailable},
		{name: "unknown field", lines: []string{":server 768 reader :{\"name\":\"#room\",\"members\":0,\"messages\":0,\"extra\":1}\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "duplicate channel", lines: []string{entry, entry, end}, wantErr: ErrReadUnavailable},
		{name: "duplicate JSON key", lines: []string{`:server 768 reader :{"name":"#room","name":"#other","members":0,"messages":0}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "case alias", lines: []string{`:server 768 reader :{"name":"#room","Members":0,"messages":0}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "null count", lines: []string{`:server 768 reader :{"name":"#room","members":null,"messages":0}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "missing count", lines: []string{`:server 768 reader :{"name":"#room","messages":0}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "trailing JSON", lines: []string{`:server 768 reader :{"name":"#room","members":0,"messages":0} {}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "direct target", lines: []string{`:server 768 reader :{"name":"alice","members":0,"messages":0}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "negative count", lines: []string{`:server 768 reader :{"name":"#room","members":-1,"messages":0}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "bad timestamp", lines: []string{`:server 768 reader :{"name":"#room","members":0,"messages":1,"last_activity":"bad"}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "missing history timestamp", lines: []string{`:server 768 reader :{"name":"#room","members":0,"messages":1}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "timestamp without history", lines: []string{`:server 768 reader :{"name":"#room","members":0,"messages":0,"last_activity":"2026-10-04T01:00:00Z"}` + "\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "registration tail", lines: []string{":server 422 reader :MOTD missing\r\n", entry, end}, want: 1, wantChannels: []core.Channel{channel}},
		{name: "native failure", lines: []string{":server 403 reader :No such channel\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "unexpected reply tags", lines: []string{"@foreign=1 " + entry, end}, wantErr: ErrReadUnavailable},
		{name: "missing completion disconnect", lines: []string{entry}, wantErr: ErrReadUnavailable},
		{name: "foreign terminator", lines: []string{entry, ":server 769 other :End of channels list\r\n"}, wantErr: ErrReadUnavailable},
		{name: "bad terminator", lines: []string{entry, ":server 769 reader :done\r\n"}, wantErr: ErrReadUnavailable},
		{name: "count boundary", lines: maxLines, want: 256},
		{name: "count overflow", lines: overLines, wantErr: store.ErrCapacity},
		{name: "wire overflow", lines: []string{entry, end}, max: len(entry) + len(end) - 1, wantErr: store.ErrCapacity},
		{name: "encoded overflow", lines: []string{escaped, end}, max: len(escaped) + len(end), wantErr: store.ErrCapacity},
		{name: "deadline", lines: []string{entry}, continuous: true, wantErr: ErrReadUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := channelsFixture(t, tc.lines, tc.continuous)
			defer stop()
			d := 2 * time.Second
			if tc.continuous {
				d = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), d)
			defer cancel()
			max := tc.max
			if max == 0 {
				max = core.MaxChannelsBytes
			}
			got, err := boundedChannelsRequest(ctx, client, max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got.Channels != nil {
					t.Fatalf("result=%+v err=%v", got, err)
				}
				return
			}
			if err != nil || len(got.Channels) != tc.want {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if got.Channels == nil || tc.wantChannels != nil && !reflect.DeepEqual(got.Channels, tc.wantChannels) {
				t.Fatalf("metadata=%+v want=%+v", got.Channels, tc.wantChannels)
			}
		})
	}
}

func channelLines(count int) []string {
	lines := make([]string, 0, count+1)
	for i := 0; i < count; i++ {
		lines = append(lines, fmt.Sprintf(":server 768 reader :{\"name\":\"#c%03d\",\"members\":0,\"messages\":0}\r\n", i))
	}
	lines = append(lines, ":server 769 reader :End of channels list\r\n")
	return lines
}
