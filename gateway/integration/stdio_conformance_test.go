//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/store"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestStdioPipeRunsFullConformanceAndSIGINTWatchShutdown(t *testing.T) {
	shortGatewayTimer(t, readLeaseTimer)
	_, c, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	p := startStdio(t, gatewayBinary(t), c)
	stop := func() {
		t.Helper()
		if err := p.command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		if err := p.wait(); err != nil {
			t.Fatalf("stdio SIGINT did not join: %v stderr=%s", err, p.stderr.String())
		}
	}
	duplexOperations(t, credentials, false, "framed_stream", 3,
		func(request frameRequest) { p.write(t, request) },
		func() adapter.FrameResponse { return p.read(t) }, stop)
	journal, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatalf("SIGINT left journal locked: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	parent := next(t, observer, func(e *irc.MessageEvent) bool { return e.Message == "TCP/TLS whole body\n🙂" })
	reply := next(t, observer, func(e *irc.MessageEvent) bool { return e.Message == "framed reply" })
	if reply.ReplyTo != parent.ID || reply.ThreadID != parent.ID || reply.From != "fixture-author" || reply.AccountID != parent.AccountID {
		t.Fatalf("native pipe reply facts: %+v parent=%+v", reply, parent)
	}

}

func TestStdioPipeExecutesReadAndReactionOperations(t *testing.T) {
	socket, c, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	if err := author.SendWithID("#gateway-fixture", "stdio context parent", "stdio-read-parent"); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "stdio-read-parent" })
	parent := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	if err := author.Chat(irc.ChatRequest{Action: "pin", Target: "#gateway-fixture", ID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "pin" && e.ID == parent.ID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	_ = author.Close()
	wantTopic := seedTopic(t, socket, "stdio topic ☃")
	wantPoll, pollID := seedAndClosePollAt(t, socket, 60)
	author = dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	if err := author.Chat(irc.ChatRequest{Action: "prepare", Target: "#gateway-fixture", ID: parent.ID, Seconds: 60, Text: "stdio waiting"}); err != nil {
		t.Fatal(err)
	}
	prepared := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := author.Chat(irc.ChatRequest{Action: "room", Target: "#gateway-fixture", Seconds: -1, Limit: -1}); err != nil {
		t.Fatal(err)
	}
	nativeRoom := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "room" && e.Target == "#gateway-fixture" })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	p := startStdio(t, gatewayBinary(t), c)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	call := func(id string, request core.Request) adapter.FrameResponse {
		t.Helper()
		id += "-" + rand.Text()
		p.write(t, frameRequest{Version: 1, CorrelationID: id, Profile: "plain", Request: credentials.token(t, request, true)})
		response := p.read(t)
		if response.Version != 1 || response.CorrelationID != id || response.Event != "result" || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
			t.Fatalf("stdio %s failed: %+v", request.Operation, response)
		}
		return response
	}
	contextResult := call("context", core.Request{Version: 1, RequestID: "stdio-context", Operation: core.ContextRead, Target: target, ParentID: parent.ID, Limit: 10})
	if contextResult.Result.Context == nil || contextResult.Result.Context.Summary.TriggerID != parent.ID || contextResult.Result.Context.Summary.RootID != parent.ID || len(contextResult.Result.Context.Messages) == 0 || contextResult.Result.Context.Messages[0].Message != parent.Message {
		t.Fatalf("stdio context missing parent: %+v", contextResult)
	}
	pinsResult := call("pins", core.Request{Version: 1, RequestID: "stdio-pins", Operation: core.PinsRead, Target: target})
	if pinsResult.Result.Pins == nil || len(pinsResult.Result.Pins.Pins) != 1 || pinsResult.Result.Pins.Pins[0].ID != parent.ID || pinsResult.Result.Pins.Pins[0].Message != parent.Message {
		t.Fatalf("stdio pins mismatch: %+v", pinsResult)
	}
	roomResult := call("room", core.Request{Version: 1, RequestID: "stdio-room", Operation: core.RoomRead, Target: target})
	if roomResult.Result.Room == nil || *roomResult.Result.Room != (core.RoomResult{Target: *target, SlowSeconds: nativeRoom.SlowSeconds, HistoryLimit: nativeRoom.HistoryLimit}) {
		t.Fatalf("stdio room mismatch: %+v", roomResult)
	}
	waitingResult := call("waiting", core.Request{Version: 1, RequestID: "stdio-waiting", Operation: core.WaitingRead, Target: target, ParentID: parent.ID})
	if waitingResult.Result.Waiting == nil || len(waitingResult.Result.Waiting.Signals) != 1 || !reflect.DeepEqual(waitingResult.Result.Waiting.Signals[0], prepared.ChatEntry) {
		t.Fatalf("stdio waiting mismatch: %+v", waitingResult)
	}
	pollResult := call("poll", core.Request{Version: 1, RequestID: "stdio-poll", Operation: core.PollResults, Target: target, PollID: pollID})
	if pollResult.Result.Poll == nil || !reflect.DeepEqual(*pollResult.Result.Poll, wantPoll) {
		t.Fatalf("stdio poll mismatch: %+v", pollResult)
	}
	topicResult := call("topic", core.Request{Version: 1, RequestID: "stdio-topic", Operation: core.TopicRead, Target: target})
	if topicResult.Result.Topic == nil || !reflect.DeepEqual(*topicResult.Result.Topic, wantTopic) {
		t.Fatalf("stdio topic mismatch: %+v", topicResult)
	}
	reaction := core.Request{Version: 1, RequestID: "stdio-reaction", Operation: core.ReactionAdd, Target: target, ParentID: parent.ID, Reaction: "🫶"}
	reactionResult := call("reaction", reaction)
	if reactionResult.Result.Result == nil || !reactionResult.Result.Result.Durable {
		t.Fatalf("stdio reaction was not admitted: %+v", reactionResult)
	}
	receiptRequest := core.Request{Version: 1, RequestID: "stdio-reaction-receipt", Operation: core.ReceiptGet, LookupID: reaction.RequestID}
	var receiptResult adapter.FrameResponse
	for deadline := time.Now().Add(5 * time.Second); ; {
		receiptResult = call("reaction-receipt", receiptRequest)
		if receiptResult.Result.Result != nil && receiptResult.Result.Result.State == core.Accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stdio reaction receipt was not accepted: %+v", receiptResult)
		}
		time.Sleep(20 * time.Millisecond)
	}
	observed := next(t, observer, func(e *irc.MessageEvent) bool { return e.ID == receiptResult.Result.Result.MessageID })
	if observed.Reaction != reaction.Reaction || observed.Message != reaction.Reaction || observed.ReplyTo != parent.ID || observed.Target != target.Name {
		t.Fatalf("stdio reaction native facts mismatch: %+v", observed)
	}
	if err := p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(); err != nil {
		t.Fatalf("stdio reads did not join: %v stderr=%s", err, p.stderr.String())
	}
}
