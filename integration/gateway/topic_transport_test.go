//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestTopicReadAcrossTransportsAndHistoryEviction(t *testing.T) {
	f := newReactionTransportFixture(t)
	defer f.framed.Close()
	defer f.ws.CloseNow()
	defer f.watch.CloseNow()
	want := seedTopic(t, f.socket, "Header café ☃ — whole UTF-8")
	live := next(t, f.observer, func(e *irc.TopicEvent) bool { return e.Channel == want.Target.Name })
	if live.Topic != want.Topic || live.SetBy != want.SetBy {
		t.Fatalf("native live topic mismatch: %+v", live)
	}
	request := core.Request{Version: 1, RequestID: "topic-read", Operation: core.TopicRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}}
	assertTopicTransports(t, f, request, want)

	marker := seedTopicHistoryMarker(t, f.socket)
	evictTopicHistory(t, f.socket, marker)
	if retained := topicHistorySnapshot(t, f.socket); retained[marker] != "" {
		t.Fatalf("topic marker survived history eviction: %s", marker)
	}
	assertTopicTransports(t, f, request, want)

	clearTopic(t, f.socket)
	want.Topic, want.SetBy, want.SetAt = "", "", time.Time{}
	assertTopicTransports(t, f, request, want)
	before := topicHistorySnapshot(t, f.socket)
	for _, tc := range []struct {
		name, path, token string
		status            int
		code              core.ErrorCode
	}{
		{"missing kind", "/v1/topic?target=%23gateway-fixture", f.bearer, http.StatusBadRequest, core.InvalidRequest},
		{"unknown query", "/v1/topic?kind=room&target=%23gateway-fixture&extra=x", f.bearer, http.StatusBadRequest, core.InvalidRequest},
		{"wrong room", "/v1/topic?kind=room&target=%23other-private", f.bearer, http.StatusForbidden, core.Forbidden},
		{"bad credential", "/v1/topic?kind=room&target=%23gateway-fixture", "not-a-bearer", http.StatusUnauthorized, core.AuthenticationFailed},
	} {
		status, body := topicBearerGETAuth(t, f, tc.path, tc.token)
		if status != tc.status {
			t.Fatalf("%s status=%d body=%s want=%d", tc.name, status, body, tc.status)
		}
		var envelope struct {
			Version int             `json:"version"`
			Error   core.Failure    `json:"error"`
			Topic   json.RawMessage `json:"topic"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Version != 1 || envelope.Error.Code != tc.code || len(envelope.Topic) != 0 {
			t.Fatalf("%s disclosed topic: %+v", tc.name, envelope.Topic)
		}
	}
	if after := topicHistorySnapshot(t, f.socket); !sameSnapshot(before, after) {
		t.Fatalf("topic reads mutated native history: before=%v after=%v", before, after)
	}
	if status, _ := topicBearerGET(t, f, "/v1/requests/topic-read"); status != http.StatusNotFound {
		t.Fatalf("topic read created delivery receipt: status=%d", status)
	}
}

func assertTopicTransports(t *testing.T, f *reactionTransportFixture, request core.Request, want core.TopicResult) {
	t.Helper()
	before := topicHistorySnapshot(t, f.socket)
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	cases := []struct {
		name string
		call func(string) (int, dispatch.DetachedResult)
	}{
		{"http1", func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, f, token, true) }},
		{"http2", func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, &h2, token, true) }},
		{"signed-http2", func(token string) (int, dispatch.DetachedResult) { return contextHTTPCall(t, &h2, token, false) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "topic-framed")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "topic-websocket")
		}},
	}
	for _, tc := range cases {
		status, result := tc.call(f.cred.token(t, request, tc.name != "signed-http2"))
		if status != http.StatusOK || result.Operation != core.TopicRead || result.Topic == nil || !reflect.DeepEqual(*result.Topic, want) {
			t.Fatalf("%s topic mismatch: status=%d result=%+v want=%+v", tc.name, status, result, want)
		}
	}
	path := "/v1/topic?kind=room&target=" + url.QueryEscape("#gateway-fixture")
	status, body := topicBearerGET(t, f, path)
	var got core.TopicResult
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("bearer topic mismatch: status=%d body=%s err=%v got=%+v want=%+v", status, body, err, got, want)
	}
	status, body = topicBearerGET(t, &h2, path)
	if err := json.Unmarshal(body, &got); status != http.StatusOK || err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP/2 bearer topic mismatch: status=%d body=%s err=%v got=%+v want=%+v", status, body, err, got, want)
	}
	if after := topicHistorySnapshot(t, f.socket); !sameSnapshot(before, after) {
		t.Fatalf("successful topic reads mutated native history: before=%v after=%v", before, after)
	}
}
