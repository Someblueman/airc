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

func TestTopicMutationCollector(t *testing.T) {
	target := &core.Target{Kind: "room", Name: "#room"}
	topic := "whole café ☃"
	request := core.Request{Version: 1, RequestID: "topic-set", Operation: core.TopicSet, Target: target, Topic: &topic}
	meta := ":server 332 reader #room :" + topic + "\r\n:server 333 reader #room reader 1234\r\n"
	cases := []struct {
		name       string
		lines      string
		want       *core.TopicResult
		code       core.ErrorCode
		continuous bool
	}{
		{name: "populated", lines: meta, want: &core.TopicResult{Target: *target, Topic: topic, SetBy: "reader", SetAt: time.Unix(1234, 0).UTC()}},
		{name: "clear", lines: ":server 331 reader #room :No topic is set\r\n", want: &core.TopicResult{Target: *target}},
		{name: "foreign target", lines: ":server 332 reader #other :topic\r\n", code: core.DeliveryUncertain},
		{name: "duplicate text", lines: strings.Replace(meta, "\r\n:server 333", "\r\n:server 332 reader #room :again\r\n:server 333", 1), code: core.DeliveryUncertain},
		{name: "missing metadata", lines: ":server 332 reader #room :topic\r\n", code: core.DeliveryUncertain},
		{name: "broadcast before acknowledgement", lines: ":reader!user@host TOPIC #room :" + topic + "\r\n" + meta, want: &core.TopicResult{Target: *target, Topic: topic, SetBy: "reader", SetAt: time.Unix(1234, 0).UTC()}},
		{name: "foreign setter", lines: strings.Replace(meta, "#room reader 1234", "#room other 1234", 1), code: core.DeliveryUncertain},
		{name: "foreign metadata room", lines: strings.Replace(meta, "333 reader #room", "333 reader #other", 1), code: core.DeliveryUncertain},
		{name: "foreign recipient", lines: strings.Replace(meta, "333 reader", "333 other", 1), code: core.DeliveryUncertain},
		{name: "metadata first", lines: ":server 333 reader #room reader 1234\r\n", code: core.DeliveryUncertain},
		{name: "noncanonical timestamp", lines: strings.Replace(meta, "1234", "+1234", 1), code: core.DeliveryUncertain},
		{name: "unrepresentable timestamp", lines: strings.Replace(meta, "1234", "9223372036854775807", 1), code: core.DeliveryUncertain},
		{name: "permanent permission denial", lines: ":server 481 reader :Permission denied\r\n", code: core.Forbidden},
		{name: "invalid input denial", lines: ":server 417 reader :Invalid topic\r\n", code: core.InvalidRequest},
		{name: "noise deadline", lines: ":server 332 reader #room :" + topic + "\r\n", code: core.DeliveryUncertain, continuous: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, commands, stop := topicMutationFixture(t, tc.lines, tc.continuous)
			defer stop()
			timeout := 3 * time.Second
			if tc.continuous {
				timeout = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			if tc.name == "clear" {
				empty := ""
				attempt.Job.Request.Topic = &empty
			}
			got := topicExchange(ctx, client, attempt)
			select {
			case command := <-commands:
				if !strings.HasPrefix(command, "TOPIC #room :") {
					t.Fatalf("outgoing topic command=%q", command)
				}
			case <-time.After(time.Second):
				t.Fatal("fixture did not observe topic command")
			}
			if tc.want != nil {
				if got.Topic == nil || got.Topic.Topic != *tc.want {
					t.Fatalf("topic=%+v completion=%+v want=%+v", got.Topic, got.Completion, tc.want)
				}
			} else if got.Completion.Failure.Code != tc.code || got.Topic != nil {
				t.Fatalf("topic=%+v completion=%+v want=%s", got.Topic, got.Completion, tc.code)
			}
		})
	}
}

func topicMutationFixture(t *testing.T, lines string, continuous bool) (*irc.Client, <-chan string, func()) {
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
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "USER ") {
				_, _ = fmt.Fprint(conn, ":server 001 reader :welcome\r\n")
			}
			if strings.HasPrefix(line, "TOPIC #room :") {
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
				return
			}
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
			t.Error("topic mutation fixture did not stop")
		}
	}
}
