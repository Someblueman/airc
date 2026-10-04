package backend

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestBoundedMembersCollector(t *testing.T) {
	valid := ":server 353 reader = #room :@Alice bob\r\n"
	end := ":server 366 reader #room :End of NAMES list\r\n"
	var overCount []string
	for i := 0; i <= core.MaxMembers; i++ {
		overCount = append(overCount, fmt.Sprintf("member%d", i))
	}
	cases := []struct {
		name       string
		lines      []string
		max        int
		continuous bool
		want       core.MembersResult
		wantErr    error
	}{
		{name: "members", lines: []string{valid, end}, want: core.MembersResult{Target: core.Target{Kind: "room", Name: "#room"}, Members: []core.Member{{Nick: "Alice", Operator: true}, {Nick: "bob"}}}},
		{name: "empty", lines: []string{end}, want: core.MembersResult{Target: core.Target{Kind: "room", Name: "#room"}, Members: []core.Member{}}},
		{name: "sorts chunks", lines: []string{":server 353 reader = #room :zulu\r\n", ":server 353 reader = #room :@Alpha\r\n", end}, want: core.MembersResult{Target: core.Target{Kind: "room", Name: "#room"}, Members: []core.Member{{Nick: "Alpha", Operator: true}, {Nick: "zulu"}}}},
		{name: "duplicate casefold", lines: []string{":server 353 reader = #room :Alice alice\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "registration noise", lines: []string{":server 422 reader :MOTD File is missing\r\n", valid, end}, want: core.MembersResult{Target: core.Target{Kind: "room", Name: "#room"}, Members: []core.Member{{Nick: "Alice", Operator: true}, {Nick: "bob"}}}},
		{name: "count overflow", lines: []string{":server 353 reader = #room :" + strings.Join(overCount, " ") + "\r\n", end}, wantErr: store.ErrCapacity},
		{name: "foreign recipient", lines: []string{strings.Replace(valid, "reader", "other", 1), end}, wantErr: ErrReadUnavailable},
		{name: "foreign completion", lines: []string{valid, strings.Replace(end, "#room", "#other", 1)}, wantErr: ErrReadUnavailable},
		{name: "native denial", lines: []string{":server 403 reader #room :No such channel\r\n"}, wantErr: ErrReadUnavailable},
		{name: "foreign room", lines: []string{strings.Replace(valid, "#room", "#other", 1), end}, wantErr: ErrReadUnavailable},
		{name: "malformed marker", lines: []string{":server 353 reader = #room :+Alice\r\n", end}, wantErr: ErrReadUnavailable},
		{name: "missing terminator", lines: []string{valid}, wantErr: ErrReadUnavailable},
		{name: "wire overflow", lines: []string{valid, end}, max: len(valid) + len(end) - 1, wantErr: store.ErrCapacity},
		{name: "continuous noise", lines: []string{valid}, continuous: true, wantErr: ErrReadUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := membersFixture(t, tc.lines, tc.continuous)
			defer stop()
			timeout := 2 * time.Second
			if tc.continuous {
				timeout = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			max := tc.max
			if max == 0 {
				max = core.MaxMembersBytes
			}
			got, err := boundedMembersRequest(ctx, client, "#room", max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || got.Target != (core.Target{}) || got.Members != nil {
					t.Fatalf("result=%+v err=%v want %v", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got.Target != tc.want.Target || len(got.Members) != len(tc.want.Members) {
				t.Fatalf("result=%+v err=%v want %+v", got, err, tc.want)
			}
			for i := range got.Members {
				if got.Members[i] != tc.want.Members[i] {
					t.Fatalf("member[%d]=%+v want %+v", i, got.Members[i], tc.want.Members[i])
				}
			}
		})
	}
}

func membersFixture(t *testing.T, lines []string, continuous bool) (*irc.Client, func()) {
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
				_, _ = fmt.Fprint(conn, ":server 001 reader :welcome\r\n")
			}
			if line != "NAMES #room" {
				continue
			}
			for _, response := range lines {
				if _, err := fmt.Fprint(conn, response); err != nil {
					return
				}
			}
			if continuous {
				for {
					if _, err := fmt.Fprint(conn, ":server NOTICE reader :noise\r\n"); err != nil {
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
			return
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	client, err := irc.DialContext(ctx, irc.Config{Nick: "reader", Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	stop := func() {
		if client != nil {
			_ = client.Close()
		}
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("members fixture did not join")
		}
	}
	if err != nil {
		stop()
		t.Fatal(err)
	}
	return client, stop
}
