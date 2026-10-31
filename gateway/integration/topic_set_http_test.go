//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

// TestTopicSetHTTPTrimClearAndNativeMetadata maps topic.set to the native
// topic: the saved acknowledgement is the trimmed native topic with its native
// setter and time, an empty topic clears both, and a maximal escaped topic
// round-trips through the request route.
func TestTopicSetHTTPTrimClearAndNativeMetadata(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	set := func(id, topic string, header bool) core.TopicResult {
		t.Helper()
		request := core.Request{Version: 1, RequestID: id, Operation: core.TopicSet, Target: contractRoom(), Topic: &topic}
		if header {
			headerPost(t, f, request)
		} else if status, admission := reactionHTTPCall(t, f, f.cred.token(t, request, true)); status != http.StatusOK && status != http.StatusAccepted || admission.Result == nil {
			t.Fatalf("request-route topic admission: %d %+v", status, admission)
		}
		saved := waitHTTPPinResult(t, f, id, core.Accepted)
		native := queryNativeTopic(t, f.socket, "#gateway-fixture")
		if saved.Topic == nil || saved.Topic.Topic != native || saved.MessageID != "" || saved.Persisted != nil || saved.RecipientConnected != nil || saved.SendAttempts != 1 || saved.LookupAttempts != 0 {
			t.Fatalf("topic acknowledgement differs from native: saved=%+v native=%+v", saved, native)
		}
		return native
	}
	if got := set("topic-http-trim", "  HTTP/1 topic ☃  ", true); got.Topic != "HTTP/1 topic ☃" || got.SetBy != "fixture-author" || got.SetAt.IsZero() {
		t.Fatalf("trimmed native topic mismatch: %+v", got)
	}
	if got := set("topic-http-clear", "", true); got != (core.TopicResult{Target: *contractRoom()}) {
		t.Fatalf("clear retained topic metadata: %+v", got)
	}
	model := strings.Repeat("<", 400)
	if got := set("topic-http-escaped", model, false); got.Topic != model {
		t.Fatalf("escaped maximal topic changed: %+v", got)
	}
}
