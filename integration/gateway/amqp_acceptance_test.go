//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestAMQPNativeSendBoundedReadsReplayAndJoinedShutdown(t *testing.T) {
	f := prepareAMQPFixture(t)
	f.start(t)
	peer := f.connect(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	var requests []core.Request
	var ids []string
	for i := 0; i < 3; i++ {
		r := core.Request{Version: 1, RequestID: fmt.Sprintf("amqp-native-%d", i), Operation: core.Send, Target: room, Message: fmt.Sprintf("AMQP-search-%d exact native body\n", i) + strings.Repeat("世界 🙂", 200)}
		issued, expiry := time.Now().Unix(), time.Now().Unix()+120
		r.IssuedAt, r.ExpiresAt = &issued, &expiry
		response := peer.call(t, f, r)
		if response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal(response)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
		if event.From != "fixture-author" || event.Target != room.Name {
			t.Fatal(event)
		}
		requests = append(requests, r)
		ids = append(ids, event.ID)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 3 {
		t.Fatal(native)
	}
	for i, r := range requests {
		if native[i].ID != ids[i] || native[i].Message != r.Message || native[i].From != "fixture-author" {
			t.Fatal(native[i])
		}
	}
	for _, op := range []core.Operation{core.HistoryRead, core.Search} {
		cursor := ""
		for i := 0; i < 4; i++ {
			r := core.Request{Version: 1, RequestID: fmt.Sprintf("amqp-%s-page-%d", strings.ReplaceAll(string(op), ".", "-"), i), Operation: op, Target: room, Limit: 1, Cursor: cursor}
			if op == core.Search {
				r.Query = "AMQP-search"
			} else if cursor == "" {
				r.Start = "retained"
			}
			response := peer.call(t, f, r)
			if response.Result == nil || response.Result.Page == nil {
				t.Fatal(response)
			}
			page := response.Result.Page
			wantStatus := "ok"
			if i < 2 {
				wantStatus = "more"
			}
			if page.Gap || page.Status != wantStatus || page.Cursor == "" {
				t.Fatal("page boundary", page)
			}
			if i < 3 {
				if len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], native[i]) {
					t.Fatal("whole bounded record", page.Messages, native[i])
				}
			} else if len(page.Messages) != 0 {
				t.Fatal("caught-up boundary", page)
			}
			cursor = page.Cursor
		}
	}
	context := peer.call(t, f, core.Request{Version: 1, RequestID: "amqp-context", Operation: core.ContextRead, Target: room, ParentID: ids[0], Limit: 1, MaxBytes: 8192})
	if context.Result == nil || context.Result.Context == nil || len(context.Result.Context.Messages) != 1 || !reflect.DeepEqual(&context.Result.Context.Messages[0], native[0]) || context.Result.Context.Summary.RootID != ids[0] || context.Result.Context.Summary.OmittedMessages != 0 {
		t.Fatal("bounded complete context", context)
	}
	// Lose the producer's observation of both the request disposition and result.
	// Native observation fences successful delivery before reconnecting.
	lost := core.Request{Version: 1, RequestID: "amqp-lost-ack", Operation: core.Send, Target: room, Message: "AMQP acknowledgement unread\nΩ 🙂"}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	lost.IssuedAt, lost.ExpiresAt = &issued, &expiry
	peer.submit(t, f.credentials.token(t, lost, false))
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == lost.Message })
	_ = peer.conn.NetConn().Close()
	reconnect := f.connect(t)
	retry := reconnect.call(t, f, lost)
	if retry.Result == nil || retry.Result.Result == nil || !retry.Result.Result.Durable {
		t.Fatal(retry)
	}
	for i, request := range append(requests, lost) {
		messageID := event.ID
		if i < 3 {
			messageID = ids[i]
		}
		reconnect.accepted(t, f, request, messageID)
	}
	f.stop(t)
	journal, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range append(requests, lost) {
		job, err := journal.Lookup(t.Context(), fixtureIdentity(t, f.config), r.RequestID)
		wantID := event.ID
		if i < 3 {
			wantID = ids[i]
		}
		if err != nil || job.Result.MessageID != wantID || job.Result.SendAttempts != 1 || !job.Result.Durable || job.Request.Message != r.Message {
			t.Fatal(job, err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	restarted := f.connect(t)
	restarted.accepted(t, f, lost, event.ID)
	f.stop(t)
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(messages) != 4 || messages[3].ID != event.ID {
		t.Fatal("native replay duplicated delivery", messages)
	}
}
