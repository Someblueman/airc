//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestTopicSetHTTP1TrimClearAndSignedHTTP2(t *testing.T) {
	f := newReactionTransportFixture(t)
	topic := "  HTTP/1 topic ☃  "
	request := core.Request{Version: 1, RequestID: "topic-http1", Operation: core.TopicSet, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Topic: &topic}
	body := requestJSON(t, request)
	httpRequest, err := http.NewRequest(http.MethodPost, f.httpURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+f.bearer)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := f.http.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	var admission core.Result
	if err := json.NewDecoder(response.Body).Decode(&admission); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.ProtoMajor != 1 || response.StatusCode != http.StatusAccepted || !admission.Durable {
		t.Fatalf("HTTP/1 topic mutation mismatch: proto=%d status=%d result=%+v", response.ProtoMajor, response.StatusCode, admission)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, raw := bearerRead(t, f, "/v1/requests/"+request.RequestID)
		if status == http.StatusOK && json.Unmarshal(raw, &admission) == nil && admission.State == core.Accepted && admission.Topic != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTP/1 topic receipt mismatch: status=%d body=%s", status, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if admission.Topic.Topic.Topic != strings.TrimSpace(topic) {
		t.Fatalf("HTTP/1 topic acknowledgement mismatch: %+v", admission)
	}
	native := queryNativeTopic(t, f.socket, "#gateway-fixture")
	if native.Topic != strings.TrimSpace(topic) {
		t.Fatalf("trimmed native topic mismatch: %+v", native)
	}
	empty := ""
	clear := core.Request{Version: 1, RequestID: "topic-http-clear", Operation: core.TopicSet, Target: request.Target, Topic: &empty}
	status, result, _ := bearerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, clear))
	if status != http.StatusAccepted || !result.Durable {
		t.Fatalf("HTTP topic clear admission failed: status=%d result=%+v", status, result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := queryNativeTopic(t, f.socket, "#gateway-fixture"); got.Topic == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native topic clear did not become visible")
		}
		time.Sleep(20 * time.Millisecond)
	}

	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	model := strings.Repeat("<", 400)
	signed := core.Request{Version: 1, RequestID: "topic-signed-http2", Operation: core.TopicSet, Target: request.Target, Topic: &model}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	signed.IssuedAt, signed.ExpiresAt = &issued, &expiry
	envelope := f.cred.validityToken(t, signed, issued, expiry, false)
	signedRequest, err := http.NewRequest(http.MethodPost, h2.httpURL+"/v1/envelopes/signed", strings.NewReader(envelope))
	if err != nil {
		t.Fatal(err)
	}
	signedRequest.Header.Set("Content-Type", "application/jose")
	signedResponse, err := h2.http.Do(signedRequest)
	if err != nil {
		t.Fatal(err)
	}
	var detached dispatch.DetachedResult
	if err := json.NewDecoder(io.LimitReader(signedResponse.Body, 64<<10)).Decode(&detached); err != nil {
		t.Fatal(err)
	}
	_ = signedResponse.Body.Close()
	if signedResponse.ProtoMajor != 2 || signedResponse.StatusCode != http.StatusOK && signedResponse.StatusCode != http.StatusAccepted || detached.Result == nil {
		t.Fatalf("signed HTTP/2 topic mutation mismatch: proto=%d status=%d result=%+v", signedResponse.ProtoMajor, signedResponse.StatusCode, detached)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, raw := bearerRead(t, &h2, "/v1/requests/"+signed.RequestID)
		var saved core.Result
		if status == http.StatusOK && json.Unmarshal(raw, &saved) == nil && saved.State == core.Accepted && saved.Topic != nil {
			native := queryNativeTopic(t, f.socket, signed.Target.Name)
			if saved.Topic.Topic != native || native.Topic != model || saved.Persisted != nil || saved.RecipientConnected != nil || saved.MessageID != "" || saved.SendAttempts != 1 || saved.LookupAttempts != 0 {
				t.Fatalf("signed HTTP/2 acknowledgement mismatch: saved=%+v native=%+v", saved, native)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("signed HTTP/2 acceptance unavailable: status=%d body=%s", status, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
