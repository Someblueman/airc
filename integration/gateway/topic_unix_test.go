//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestTopicReadUnixTransportAndDenials(t *testing.T) {
	conn, credentials, stop, _, upstream := unixStreamFixture(t)
	defer conn.Close()
	defer stop()
	want := seedTopic(t, upstream, "Unix topic café ☃")
	before := topicHistorySnapshot(t, upstream)
	request := core.Request{Version: 1, RequestID: "unix-topic", Operation: core.TopicRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: credentials.token(t, request, true)})
	response := readFrame(t, conn)
	if response.Error != nil || response.Result == nil || response.Result.Topic == nil || !reflect.DeepEqual(*response.Result.Topic, want) {
		t.Fatalf("Unix topic mismatch: %+v want=%+v", response, want)
	}
	if response.Result.Operation != core.TopicRead {
		t.Fatalf("Unix topic operation=%s", response.Result.Operation)
	}
	if after := topicHistorySnapshot(t, upstream); !reflect.DeepEqual(before, after) {
		t.Fatal("Unix topic read mutated history")
	}
	marker := seedTopicHistoryMarker(t, upstream)
	evictTopicHistory(t, upstream, marker)
	before = topicHistorySnapshot(t, upstream)
	request.RequestID = "unix-topic-after-eviction"
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: credentials.token(t, request, true)})
	response = readFrame(t, conn)
	if response.Error != nil || response.Result == nil || response.Result.Topic == nil || !reflect.DeepEqual(*response.Result.Topic, want) {
		t.Fatalf("Unix topic after eviction mismatch: %+v want=%+v", response, want)
	}
	clearTopic(t, upstream)
	request.RequestID = "unix-topic-clear"
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: credentials.token(t, request, true)})
	response = readFrame(t, conn)
	if response.Error != nil || response.Result == nil || response.Result.Topic == nil || response.Result.Topic.Topic != "" || response.Result.Topic.SetBy != "" || !response.Result.Topic.SetAt.IsZero() {
		t.Fatalf("Unix topic clear mismatch: %+v", response)
	}
	if *response.Result.Topic != (core.TopicResult{Target: *request.Target}) {
		t.Fatalf("Unix empty topic lost target: %+v", response.Result.Topic)
	}
	if after := topicHistorySnapshot(t, upstream); !reflect.DeepEqual(before, after) {
		t.Fatal("Unix post-eviction/empty topic reads mutated history")
	}
	wrong := request
	wrong.RequestID = "unix-topic-wrong-room"
	wrong.Target = &core.Target{Kind: "room", Name: "#other-private"}
	writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: wrong.RequestID, Profile: "plain", Request: credentials.token(t, wrong, true)})
	response = readFrame(t, conn)
	if response.Error == nil || response.Error.Code != core.Forbidden || response.Result != nil {
		t.Fatalf("Unix wrong-room topic was not denied: %+v", response)
	}
	if after := topicHistorySnapshot(t, upstream); !reflect.DeepEqual(before, after) {
		t.Fatal("Unix denied read changed history")
	}
	for _, id := range []string{"unix-topic", "unix-topic-after-eviction", "unix-topic-clear", "unix-topic-wrong-room"} {
		lookup := core.Request{Version: 1, RequestID: "receipt-" + id, Operation: core.ReceiptGet, LookupID: id}
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: lookup.RequestID, Profile: "plain", Request: credentials.token(t, lookup, true)})
		result := readFrame(t, conn)
		if result.Error == nil || result.Error.Code != core.InvalidRequest || result.Result != nil {
			t.Fatalf("Unix topic read created receipt: %+v", result)
		}
	}
}
