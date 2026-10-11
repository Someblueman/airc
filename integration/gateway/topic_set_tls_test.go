//go:build darwin || linux

package gateway_test

import (
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestTopicSetOverFramedTLS(t *testing.T) {
	conn, credentials, stop, socket := framedTLSCardFixture(t)
	defer conn.Close()
	defer stop()
	call := func(request core.Request, correlation string) adapter.FrameResponse {
		writeFrame(t, conn, frameRequest{Version: 1, CorrelationID: correlation, Profile: "plain", Request: credentials.token(t, request, true)})
		response := readFrame(t, conn)
		if response.CorrelationID != correlation || response.Event != "result" {
			t.Fatalf("TLS topic response mismatch: %+v", response)
		}
		return response
	}
	topic := "TLS durable topic — ☃"
	request := topicMutationRequest(t, "tls-topic-set", &core.Target{Kind: "room", Name: "#gateway-fixture"}, topic)
	result := assertTopicSet(t, call, socket, request)
	if result.Topic == nil || result.Topic.Topic.Topic != topic || result.Topic.Topic.SetBy != "fixture-author" || result.Topic.Topic.SetAt.IsZero() {
		t.Fatalf("TLS topic acknowledgement lost native metadata: %+v", result.Topic)
	}
	empty := ""
	clear := topicMutationRequest(t, "tls-topic-clear", request.Target, empty)
	cleared := assertTopicSet(t, call, socket, clear)
	if cleared.Topic == nil || !reflect.DeepEqual(cleared.Topic.Topic, core.TopicResult{Target: *request.Target}) {
		t.Fatalf("TLS clear acknowledgement mismatch: %+v", cleared.Topic)
	}
}
