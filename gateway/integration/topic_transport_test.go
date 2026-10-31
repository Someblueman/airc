//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestTopicReadSurvivesEvictionAndClear maps topic.read to the native topic
// with its setter and time, independent of message history, and reads an
// empty topic after a native clear.
func TestTopicReadSurvivesEvictionAndClear(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	want := seedTopic(t, f.socket, "Header café ☃ — whole UTF-8")
	if live := next(t, f.observer, func(e *irc.TopicEvent) bool { return e.Channel == want.Target.Name }); live.Topic != want.Topic || live.SetBy != want.SetBy {
		t.Fatalf("native live topic mismatch: %+v", live)
	}
	read := func(label string) {
		t.Helper()
		request := core.Request{Version: 1, RequestID: "topic-read-" + label, Operation: core.TopicRead, Target: contractRoom()}
		if status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true)); status != http.StatusOK || result.Topic == nil || !reflect.DeepEqual(*result.Topic, want) {
			t.Fatalf("%s topic mismatch: status=%d result=%+v want=%+v", label, status, result, want)
		}
		status, body := topicHeaderGET(t, f, "/v1/topic?kind=room&target="+url.QueryEscape("#gateway-fixture"))
		var got core.TopicResult
		if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s GET topic mismatch: status=%d body=%s want=%+v", label, status, body, want)
		}
	}
	read("set")
	marker := seedTopicHistoryMarker(t, f.socket)
	evictTopicHistory(t, f.socket, marker)
	read("evicted")
	clearTopic(t, f.socket)
	want.Topic, want.SetBy, want.SetAt = "", "", time.Time{}
	read("cleared")
}
