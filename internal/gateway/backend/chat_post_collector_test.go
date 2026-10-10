package backend

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/protocol"
)

func TestChatPostCollectorRequiresCompleteMatchingEvidence(t *testing.T) {
	meta := protocol.MessageMetadata{ChatMetadata: protocol.ChatMetadata{AccountID: strings.Repeat("b", 32), Kind: "action"}, ID: strings.Repeat("a", 32), Seq: 1, From: "reader", Target: "#room", Message: "whole body", Timestamp: time.Now().UTC()}
	receipt, entry, end := chatPostWire(t, meta)
	changed := meta
	changed.Timestamp = changed.Timestamp.Add(time.Second)
	_, differentEntry, _ := chatPostWire(t, changed)
	for _, tc := range []struct {
		name, wire string
		accepted   bool
		code       core.ErrorCode
		may        bool
		upstream   int
	}{
		{name: "whole", wire: receipt + entry + end, accepted: true},
		{name: "receipt-only", wire: receipt, code: core.DeliveryUncertain, may: true},
		{name: "missing-end", wire: receipt + entry, code: core.DeliveryUncertain, may: true},
		{name: "reordered", wire: entry + receipt + end, code: core.DeliveryUncertain, may: true},
		{name: "duplicate-receipt", wire: receipt + receipt + entry + end, code: core.DeliveryUncertain, may: true},
		{name: "mismatched-timestamp", wire: receipt + differentEntry + end, code: core.DeliveryUncertain, may: true},
		{name: "malformed-entry", wire: receipt + ":server 777 reader :{bad}\r\n" + end, code: core.DeliveryUncertain, may: true},
		{name: "permanent-before-evidence", wire: ":server 461 reader :invalid\r\n", code: core.InvalidRequest, upstream: 461},
		{name: "numeric-after-receipt", wire: receipt + ":server 461 reader :contradiction\r\n", code: core.DeliveryUncertain, may: true, upstream: 461},
		{name: "numeric-after-results", wire: receipt + entry + ":server 485 reader :contradiction\r\n", code: core.DeliveryUncertain, may: true, upstream: 485},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, writes, stop := chatPostWireFixture(t, tc.wire)
			defer stop()
			request := core.Request{Version: 1, RequestID: "post-test", Operation: core.MessageAction, Target: &core.Target{Kind: "room", Name: "#room"}, Message: meta.Message}
			attempt := store.Attempt{Kind: store.SendAttempt, Deadline: time.Now().Add(2 * time.Second), Job: store.Job{Request: request}}
			got := chatPostExchange(context.Background(), client, attempt)
			if tc.accepted {
				if got.Receipt == nil || got.Completion.Failure.Code != "" {
					t.Fatalf("positive ACK failed: %+v", got)
				}
			} else if got.Receipt != nil || got.Completion.Failure.Code != tc.code || got.Completion.MayHavePosted != tc.may || got.Completion.Failure.UpstreamCode != tc.upstream {
				t.Fatalf("malformed ACK classification: %+v", got)
			}
			select {
			case line := <-writes:
				if !strings.Contains(line, `"action":"action"`) {
					t.Fatalf("unexpected write:%s", line)
				}
			case <-time.After(time.Second):
				t.Fatal("native write not observed")
			}
		})
	}
}
