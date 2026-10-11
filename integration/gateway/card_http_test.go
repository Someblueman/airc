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

func TestProfileAndPresenceHeadersHTTP1AndHTTP2(t *testing.T) {
	f := newReactionHTTPFixture(t)
	h2 := *f
	transport := f.http.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	t.Cleanup(transport.CloseIdleConnections)
	h2.http = &http.Client{Transport: transport, Timeout: f.http.Timeout}
	model := "authorSpec-model"
	request := core.Request{Version: 1, RequestID: "authorSpec-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model}}
	status, result, location := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, request))
	if status != http.StatusAccepted || location != "/v1/requests/"+request.RequestID || result.Card != nil {
		t.Fatalf("authorSpec profile admission mismatch: status=%d location=%q result=%+v", status, location, result)
	}
	var accepted core.Result
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, raw := headerRead(t, f, "/v1/requests/"+request.RequestID)
		if status == http.StatusOK && json.Unmarshal(raw, &accepted) == nil && accepted.State == core.Accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("authorSpec profile receipt not accepted: status=%d body=%s", status, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if accepted.Card == nil || accepted.Card.Card.Model != model || accepted.MessageID != "" || accepted.Persisted != nil || accepted.RecipientConnected != nil {
		t.Fatalf("authorSpec profile receipt invented message facts: %+v", accepted)
	}
	for name, client := range map[string]*reactionTransportFixture{"http1": f, "http2": &h2} {
		status, raw := headerRead(t, client, "/v1/requests/"+request.RequestID)
		var got core.Result
		if status != http.StatusOK || json.Unmarshal(raw, &got) != nil || got.Card == nil || !reflect.DeepEqual(*got.Card, *accepted.Card) {
			t.Fatalf("%s authorSpec receipt mismatch: status=%d body=%s", name, status, raw)
		}
	}
	signed := request
	signed.RequestID = "signed-profile-http2"
	signedHTTP, err := http.NewRequest(http.MethodPost, f.httpURL+"/v1/request", strings.NewReader(f.cred.token(t, signed, false)))
	if err != nil {
		t.Fatal(err)
	}
	signedHTTP.Header.Set("Content-Type", "application/json")
	signedResponse, err := h2.http.Do(signedHTTP)
	if err != nil {
		t.Fatal(err)
	}
	var signedResult dispatch.DetachedResult
	if err := json.NewDecoder(signedResponse.Body).Decode(&signedResult); err != nil {
		t.Fatal(err)
	}
	_ = signedResponse.Body.Close()
	if signedResponse.ProtoMajor != 2 || signedResponse.StatusCode != http.StatusOK && signedResponse.StatusCode != http.StatusAccepted || signedResult.Result == nil {
		t.Fatalf("signed HTTP/2 profile failed: proto=%d status=%d result=%+v", signedResponse.ProtoMajor, signedResponse.StatusCode, signedResult)
	}
	signedAccepted := pollHeaderReaction(t, f, signed.RequestID)
	if signedAccepted.Card == nil || signedAccepted.Card.Card.Model != model || signedAccepted.SendAttempts != 1 {
		t.Fatalf("signed HTTP/2 mutation not applied: %+v", signedAccepted)
	}
	changed := request
	changed.Profile = &core.ProfilePatch{Model: func() *string { value := "conflict"; return &value }()}
	status, _, _ = headerReactionHTTP(t, f, http.MethodPut, "/v1/requests/"+request.RequestID, requestJSON(t, changed))
	if status != http.StatusConflict {
		t.Fatalf("profile request ID conflict status=%d", status)
	}
	presence := core.Request{Version: 1, RequestID: "authorSpec-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "thinking", Note: "http presence", TTLSeconds: 60}}
	status, result, _ = headerReactionHTTP(t, &h2, http.MethodPost, "/v1/messages", requestJSON(t, presence))
	if status != http.StatusAccepted || result.Card != nil {
		t.Fatalf("HTTP/2 presence admission mismatch: status=%d result=%+v", status, result)
	}
	var presenceReceipt core.Result
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, raw := headerRead(t, &h2, "/v1/requests/"+presence.RequestID)
		if status == http.StatusOK && json.Unmarshal(raw, &presenceReceipt) == nil && presenceReceipt.State == core.Accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("HTTP/2 presence receipt mismatch: status=%d body=%s", status, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if presenceReceipt.Card == nil || presenceReceipt.Card.Card.State != "thinking" || presenceReceipt.Card.Card.Note != "http presence" || presenceReceipt.Card.Card.ExpiresAt.Sub(presenceReceipt.Card.Card.LastSeen) != 60*time.Second {
		t.Fatalf("HTTP/2 presence receipt card mismatch: %+v", presenceReceipt)
	}
	observed := nativeCard(t, f.socket, "fixture-author")
	if observed.State != presenceReceipt.Card.Card.State || observed.Note != presenceReceipt.Card.Card.Note || !observed.ExpiresAt.Equal(presenceReceipt.Card.Card.ExpiresAt) {
		t.Fatalf("HTTP/2 presence differs from native: %+v", observed)
	}
}
