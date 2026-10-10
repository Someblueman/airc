//go:build darwin || linux

package gateway_test

import (
	"crypto/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

type topicSetFrameCall func(core.Request, string) adapter.FrameResponse

func topicMutationRequest(t *testing.T, id string, target *core.Target, text string) core.Request {
	t.Helper()
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	return core.Request{Version: 1, RequestID: id, Operation: core.TopicSet, Target: target, Topic: &text, IssuedAt: &issued, ExpiresAt: &expiry}
}

func waitTopicSetReceipt(t *testing.T, call topicSetFrameCall, request core.Request) *core.Result {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		lookup := core.Request{Version: 1, RequestID: "receipt-" + request.RequestID, Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := call(lookup, rand.Text())
		if response.Error == nil && response.Result != nil && response.Result.Result != nil && response.Result.Result.State == core.Accepted {
			if response.Result.Result.Topic == nil || response.Result.Result.MessageID != "" || response.Result.Result.Persisted != nil || response.Result.Result.RecipientConnected != nil {
				t.Fatalf("topic receipt invented message facts: %+v", response.Result.Result)
			}
			return response.Result.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("topic receipt was not accepted: %s", request.RequestID)
	return nil
}

func assertTopicSet(t *testing.T, call topicSetFrameCall, socket string, request core.Request) *core.Result {
	t.Helper()
	response := call(request, request.RequestID)
	if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("topic.set was not durably admitted: %+v", response)
	}
	result := waitTopicSetReceipt(t, call, request)
	native := queryTopicWithNick(t, socket, request.Target.Name, "topic-reader-"+request.RequestID)
	if result.Topic == nil || !reflect.DeepEqual(result.Topic.Topic, native) {
		t.Fatalf("topic receipt/native mismatch: receipt=%+v native=%+v", result.Topic, native)
	}
	return result
}

func queryTopicWithNick(t *testing.T, socket, target, nick string) core.TopicResult {
	t.Helper()
	c := dial(t, socket, nick, "", false, false)
	defer c.Close()
	if err := c.Topic(target); err != nil {
		t.Fatal(err)
	}
	event := next(t, c, func(e *irc.TopicEvent) bool { return e.Channel == target })
	result := core.TopicResult{Target: core.Target{Kind: "room", Name: target}, Topic: event.Topic}
	if event.Topic == "" {
		return result
	}
	raw := next(t, c, func(e *irc.RawEvent) bool { return e.Command == "333" })
	if len(raw.Params) != 4 || raw.Params[0] != c.Nick() || raw.Params[1] != target {
		t.Fatalf("native topic metadata shape: %+v", raw)
	}
	seconds, err := strconv.ParseInt(raw.Params[3], 10, 64)
	if err != nil {
		t.Fatalf("native topic timestamp %q: %v", raw.Params[3], err)
	}
	result.SetBy = raw.Params[2]
	result.SetAt = time.Unix(seconds, 0).UTC()
	return result
}

func TestTopicSetOverStdioWithRestartReplay(t *testing.T) {
	socket, config, credentials, observer := stdioFixtureConfig(t)
	defer observer.Close()
	process := startStdio(t, gatewayBinary(t), config)
	tokens := make(map[string]string)
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		token := credentials.token(t, request, true)
		if request.Operation == core.TopicSet {
			if cached := tokens[request.RequestID]; cached != "" {
				token = cached
			} else {
				tokens[request.RequestID] = token
			}
		}
		process.write(t, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: token})
		response := process.read(t)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("stdio topic response mismatch: %+v", response)
		}
		return response
	}
	topic := "stdio durable topic ☃"
	request := topicMutationRequest(t, "stdio-topic-set", &core.Target{Kind: "room", Name: "#gateway-fixture"}, topic)
	first := assertTopicSet(t, call, socket, request)
	stored := *first.Topic
	seedTopic(t, socket, "native intervening topic")
	if native := queryTopicWithNick(t, socket, request.Target.Name, "topic-reader-intervening"); native.Topic != "native intervening topic" {
		t.Fatalf("native intervening update did not apply: %+v", native)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not stop for replay: %v", err)
	}
	process = startStdio(t, gatewayBinary(t), config)
	replay := call(request, "stdio-topic-replay")
	if replay.Error != nil || replay.Result == nil || replay.Result.Result == nil || !replay.Result.Result.Durable {
		t.Fatalf("replaying original topic token was not durable: %+v", replay)
	}
	receipt := waitTopicSetReceipt(t, call, request)
	if receipt.SendAttempts != 1 || receipt.Topic == nil || !reflect.DeepEqual(*receipt.Topic, stored) {
		t.Fatalf("stdio replay changed durable topic receipt: %+v want=%+v", receipt, stored)
	}
	if native := queryTopicWithNick(t, socket, request.Target.Name, "topic-reader-replay"); native.Topic != "native intervening topic" {
		t.Fatalf("replay unexpectedly rewrote native topic: %+v", native)
	}
	empty := ""
	clear := topicMutationRequest(t, "stdio-topic-clear", request.Target, empty)
	cleared := assertTopicSet(t, call, socket, clear)
	if cleared.Topic.Topic.Topic != "" || !cleared.Topic.Topic.SetAt.IsZero() || cleared.Topic.Topic.SetBy != "" {
		t.Fatalf("stdio clear acknowledgement retained metadata: %+v", cleared.Topic)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := process.wait(); err != nil {
		t.Fatalf("stdio gateway did not stop: %v", err)
	}
}

func TestTopicSetOverUnixFramed(t *testing.T) {
	conn, credentials, stop, _, socket := unixStreamFixture(t)
	defer conn.Close()
	defer stop()
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("Unix topic response mismatch: %+v", response)
		}
		return response
	}
	topic := "Unix durable topic café"
	request := topicMutationRequest(t, "unix-topic-set", &core.Target{Kind: "room", Name: "#gateway-fixture"}, topic)
	assertTopicSet(t, call, socket, request)
	empty := ""
	clear := topicMutationRequest(t, "unix-topic-clear", request.Target, empty)
	cleared := assertTopicSet(t, call, socket, clear)
	if cleared.Topic == nil || cleared.Topic.Topic.Topic != "" || !cleared.Topic.Topic.SetAt.IsZero() {
		t.Fatalf("Unix clear acknowledgement retained topic metadata: %+v", cleared.Topic)
	}
}
