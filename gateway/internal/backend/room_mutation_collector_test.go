package backend

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRoomMutationCollector(t *testing.T) {
	target := &core.Target{Kind: "room", Name: "#room"}
	slow, limit := int64(12), 80
	request := core.Request{Version: 1, RequestID: "room-set", Operation: core.RoomUpdate, Target: target, Room: &core.RoomPatch{SlowSeconds: &slow, HistoryLimit: &limit}}
	valid := chatLine(t, irc.ChatEntry{Action: "room", Target: target.Name, SlowSeconds: slow, HistoryLimit: limit})
	cases := []struct {
		name  string
		lines string
		want  *core.RoomResult
		code  core.ErrorCode
	}{
		{name: "valid", lines: valid, want: &core.RoomResult{Target: *target, SlowSeconds: slow, HistoryLimit: limit}},
		{name: "foreign", lines: chatLine(t, irc.ChatEntry{Action: "room", Target: "#other", SlowSeconds: slow, HistoryLimit: limit}), code: core.DeliveryUncertain},
		{name: "duplicate", lines: valid + valid, code: core.DeliveryUncertain},
		{name: "mixed fields", lines: chatLine(t, irc.ChatEntry{Action: "room", Target: target.Name, Text: "unexpected"}), code: core.DeliveryUncertain},
		{name: "malformed", lines: ":server 777 reader :{bad-json\r\n", code: core.DeliveryUncertain},
		{name: "forbidden", lines: ":server 481 reader :operator required\r\n", code: core.Forbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, commands, stop := roomMutationFixture(t, tc.lines, false)
			defer stop()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			got := roomExchange(ctx, client, attempt)
			select {
			case command := <-commands:
				if !strings.HasPrefix(command, `CHAT :{"action":"room","target":"#room","seconds":12,"limit":80}`) {
					t.Fatalf("outgoing room command=%q", command)
				}
			case <-time.After(time.Second):
				t.Fatal("fixture did not observe room command")
			}
			if tc.want != nil {
				if got.Room == nil || got.Room.Room != *tc.want {
					t.Fatalf("room=%+v completion=%+v want=%+v", got.Room, got.Completion, tc.want)
				}
			} else if got.Room != nil || got.Completion.Failure.Code != tc.code {
				t.Fatalf("room=%+v completion=%+v want=%s", got.Room, got.Completion, tc.code)
			}
		})
	}
}

func TestRoomMutationCollectorDeadlineUnderNoise(t *testing.T) {
	seconds := int64(12)
	request := core.Request{Version: 1, RequestID: "room-noise", Operation: core.RoomUpdate, Target: &core.Target{Kind: "room", Name: "#room"}, Room: &core.RoomPatch{SlowSeconds: &seconds}}
	line := chatLine(t, irc.ChatEntry{Action: "room", Target: "#room", SlowSeconds: seconds})
	client, _, stop := roomMutationFixture(t, line, true)
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(time.Second), Job: store.Job{Request: request}}
	got := roomExchange(ctx, client, attempt)
	if got.Room != nil || got.Completion.Failure.Code != core.DeliveryUncertain || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("noise deadline result=%+v completion=%+v ctx=%v", got.Room, got.Completion, ctx.Err())
	}
}

func roomMutationFixture(t *testing.T, lines string, continuous bool) (*irc.Client, <-chan string, func()) {
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
				_, _ = fmt.Fprint(conn, ":server 005 reader CHAT=1 :features\r\n:server 001 reader :welcome\r\n")
			}
			if !strings.HasPrefix(line, "CHAT :") {
				continue
			}
			commands <- line
			_, _ = fmt.Fprint(conn, lines)
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
		_ = listener.Close()
		t.Fatal(err)
	}
	return client, commands, func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("room mutation fixture did not stop")
		}
	}
}
