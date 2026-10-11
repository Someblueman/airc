//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestTopicSetAcrossTransportsAndHeaders(t *testing.T) {
	f := newReactionTransportFixture(t)
	topic := "gateway topic ☃"
	request := core.Request{Version: 1, RequestID: "topic-set", Operation: core.TopicSet, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Topic: &topic}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request.IssuedAt, request.ExpiresAt = &issued, &expiry
	token := f.cred.validityToken(t, request, issued, expiry, true)
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(s string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, s) }},
		{"framed", func(s string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, s, "topic-set-framed")
		}},
		{"websocket", func(s string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, s, "topic-set-websocket")
		}},
	} {
		status, result := call.fn(token)
		if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil {
			t.Fatalf("%s topic admission failed: status=%d result=%+v", call.name, status, result)
		}
	}
	var receipt core.Result
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, result := reactionFramedCall(t, f.framed, f.cred.token(t, core.Request{Version: 1, RequestID: "topic-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, true), "topic-receipt")
		if status == http.StatusOK && result.Result != nil && result.Result.Topic != nil {
			receipt = *result.Result
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("topic receipt unavailable: status=%d result=%+v", status, result)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if receipt.Topic == nil || receipt.Topic.Topic.Topic != topic || receipt.MessageID != "" || receipt.Persisted != nil || receipt.RecipientConnected != nil {
		t.Fatalf("topic receipt invented message facts: %+v", receipt)
	}
	native := queryNativeTopic(t, f.socket, "#gateway-fixture")
	if native.Topic != topic || native.SetBy != "fixture-author" || native.SetAt.IsZero() {
		t.Fatalf("native topic mismatch: %+v", native)
	}
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	status, authorSpec, location := headerReactionHTTP(t, &h2, http.MethodPost, "/v1/messages", requestJSON(t, request))
	if status != http.StatusAccepted || location != "/v1/requests/"+request.RequestID || authorSpec.Topic == nil || authorSpec.State != core.Accepted || authorSpec.SendAttempts != 1 || authorSpec.MessageID != "" {
		t.Fatalf("authorSpec topic replay mismatch: status=%d location=%q result=%+v", status, location, authorSpec)
	}
	status, raw := headerRead(t, &h2, "/v1/requests/"+request.RequestID)
	var got core.Result
	if status != http.StatusOK || json.Unmarshal(raw, &got) != nil || got.Topic == nil || !reflect.DeepEqual(*got.Topic, *receipt.Topic) {
		t.Fatalf("HTTP/2 authorSpec topic receipt mismatch: status=%d body=%s", status, raw)
	}
	changed := request
	other := "different topic"
	changed.Topic = &other
	status, _, _ = headerReactionHTTP(t, &h2, http.MethodPut, "/v1/requests/"+request.RequestID, requestJSON(t, changed))
	if status != http.StatusConflict {
		t.Fatalf("topic request ID conflict status=%d", status)
	}
	for name, badTopic := range map[string]string{"oversized": strings.Repeat("x", 401), "control": "bad\x00topic"} {
		bad := core.Request{Version: 1, RequestID: "topic-invalid-" + name, Operation: core.TopicSet, Target: request.Target, Topic: &badTopic}
		status, _ := reactionHTTPCall(t, f, f.cred.token(t, bad, true))
		if status == http.StatusOK || status == http.StatusAccepted {
			t.Fatalf("%s topic was admitted", name)
		}
		if status, _ := headerRead(t, f, "/v1/requests/"+bad.RequestID); status != http.StatusNotFound {
			t.Fatalf("%s invalid topic created receipt: %d", name, status)
		}
	}
	for name, mutate := range map[string]func(*core.Request){
		"message":  func(r *core.Request) { r.Message = "body" },
		"reaction": func(r *core.Request) { r.Reaction = "agree" },
		"parent":   func(r *core.Request) { r.ParentID = strings.Repeat("a", 32) },
	} {
		bad := request
		bad.RequestID = "topic-foreign-field-" + name
		mutate(&bad)
		status, _, _ := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, bad))
		if status != http.StatusBadRequest {
			t.Fatalf("foreign %s status=%d", name, status)
		}
		if status, _ := headerRead(t, f, "/v1/requests/"+bad.RequestID); status != http.StatusNotFound {
			t.Fatalf("foreign %s created job status=%d", name, status)
		}
	}
	foreign := request
	foreign.RequestID = "topic-foreign"
	foreign.Target = &core.Target{Kind: "room", Name: "#other-private"}
	status, _ = reactionHTTPCall(t, f, f.cred.token(t, foreign, true))
	if status == http.StatusOK || status == http.StatusAccepted {
		t.Fatalf("foreign topic target admitted")
	}
	if status, _ := headerRead(t, f, "/v1/requests/"+foreign.RequestID); status != http.StatusNotFound {
		t.Fatalf("foreign target created job status=%d", status)
	}
}
