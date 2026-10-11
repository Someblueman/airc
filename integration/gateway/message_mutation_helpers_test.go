//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func messageMutationRequest(id string, op core.Operation, target *core.Target, parent string) core.Request {
	r := core.Request{Version: 1, RequestID: id, Operation: op, Target: target, ParentID: parent, Message: "whole message 🙂\nsecond line"}
	if op == core.MessageRetract {
		r.Message = ""
	}
	return r
}

func assertMessageMutationFacts(t *testing.T, result core.Result, request core.Request, observed *irc.MessageEvent) {
	t.Helper()
	if result.State != core.Accepted || !result.Durable || result.Accepted == nil || !*result.Accepted || result.MessageID == "" || result.Persisted == nil || !*result.Persisted || result.SendAttempts != 1 || result.LookupAttempts != 0 || result.Poll != nil || result.ReplySignal != nil {
		t.Fatalf("message mutation facts: %+v", result)
	}
	expected := request.Message
	if request.Operation == core.MessageRetract && expected == "" {
		expected = "Retracted"
	}
	if observed.ID != result.MessageID || observed.Kind != strings.TrimPrefix(string(request.Operation), "message.") || observed.Message != expected || observed.From != "fixture-author" || observed.AccountID == "" || observed.Target != request.Target.Name || observed.Supersedes != request.ParentID || observed.ReplyTo != request.ParentID || observed.Retracted {
		t.Fatalf("native message mismatch: %+v request=%+v", observed, request)
	}
	if request.ParentID != "" && observed.ThreadID == "" {
		t.Fatal("correction lost native thread root")
	}
}

func assertNoMessageMutation(t *testing.T, observer *irc.Client) {
	t.Helper()
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-observer.Events():
			if !ok {
				t.Fatal("message observer disconnected")
			}
			if message, ok := event.(*irc.MessageEvent); ok && (message.Kind == "action" || message.Kind == "correct" || message.Kind == "retract") {
				t.Fatalf("unexpected native mutation: %+v", message)
			}
		case <-timer.C:
			return
		}
	}
}

func compareMessageHistory(t *testing.T, f *reactionTransportFixture, request core.Request, observed *irc.MessageEvent) {
	t.Helper()
	retained := reactionHistory(t, f, request, observed.ID)
	// History may annotate the original after subsequent mutations. This call
	// follows each fresh post before a later mutation can annotate it.
	if !reflect.DeepEqual(retained.ChatMetadata, observed.ChatMetadata) || retained.ID != observed.ID || retained.Message != observed.Message || retained.ReplyTo != observed.ReplyTo || retained.ThreadID != observed.ThreadID || !retained.Timestamp.Equal(observed.Timestamp) {
		t.Fatalf("gateway history mismatch: %+v native=%+v", retained, observed)
	}
}
