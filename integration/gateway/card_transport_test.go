//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestProfileUpdateCardAcknowledgementAcrossTransports(t *testing.T) {
	f := newReactionTransportFixture(t)
	model, workspace, tools, about := "card-model", "card-workspace", "card-tools", "profile 🙂"
	request := core.Request{Version: 1, RequestID: "card-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model, Workspace: &workspace, Tools: &tools, About: &about}}
	if err := request.Normalize(); err != nil {
		t.Fatalf("profile request did not normalize: %v", err)
	}
	token := f.cred.token(t, request, true)
	var expected core.CardAcknowledgement
	calls := []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(s string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, s) }},
		{"framed", func(s string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, s, "card-framed")
		}},
		{"websocket", func(s string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, s, "card-websocket")
		}},
	}
	for _, call := range calls {
		status, result := call.fn(token)
		if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil {
			t.Fatalf("%s profile update failed: status=%d result=%+v", call.name, status, result)
		}
	}
	var receipt dispatch.DetachedResult
	for deadline := time.Now().Add(5 * time.Second); ; {
		status, got := reactionFramedCall(t, f.framed, f.cred.token(t, core.Request{Version: 1, RequestID: "card-profile-receipt", Operation: core.ReceiptGet, LookupID: request.RequestID}, true), "card-receipt")
		if status == http.StatusOK && got.Result != nil && got.Result.Card != nil {
			receipt = got
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("profile receipt card unavailable: status=%d result=%+v", status, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	expected = *receipt.Result.Card
	for _, call := range calls {
		status, result := call.fn(token)
		if status != http.StatusAccepted && status != http.StatusOK || result.Result == nil || result.Result.Card == nil || !reflect.DeepEqual(*result.Result.Card, expected) || result.Result.SendAttempts != 1 {
			t.Fatalf("%s accepted replay changed card: status=%d result=%+v", call.name, status, result)
		}
	}
	if expected.Card.Model != model || expected.Card.Workspace != workspace || expected.Card.Tools != tools || expected.Card.About != about || expected.Card.Nick != "fixture-author" || expected.Card.AccountID == "" {
		t.Fatalf("profile acknowledgement lost fields: %+v", expected)
	}
	if receipt.Result.MessageID != "" || receipt.Result.Persisted != nil {
		t.Fatalf("profile receipt invented message facts: %+v", receipt.Result)
	}
	directory := nativeCard(t, f.socket, "fixture-author")
	if directory.Model != model || directory.Workspace != workspace || directory.Tools != tools || directory.About != about {
		t.Fatalf("native directory did not retain profile: %+v", directory)
	}
}

func TestPresenceSetCardAcknowledgementAndClear(t *testing.T) {
	f := newReactionTransportFixture(t)
	note := "presence note 🙂"
	request := core.Request{Version: 1, RequestID: "card-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "thinking", Note: note, TTLSeconds: 60}}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil {
		t.Fatalf("presence update failed: status=%d result=%+v", status, result)
	}
	accepted := pollBearerReaction(t, f, request.RequestID)
	if accepted.Card == nil || accepted.Card.Card.State != "thinking" || accepted.Card.Card.Note != note || accepted.Card.Card.ExpiresAt.Sub(accepted.Card.Card.LastSeen) != 60*time.Second {
		t.Fatalf("presence acknowledgement mismatch: %+v", accepted)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		card := nativeCard(t, f.socket, "fixture-author")
		if card.State == "thinking" && card.Note == note && !card.ExpiresAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native presence did not appear: %+v", card)
		}
		time.Sleep(20 * time.Millisecond)
	}
	clear := core.Request{Version: 1, RequestID: "card-presence-clear", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "clear"}}
	status, result = reactionFramedCall(t, f.framed, f.cred.token(t, clear, true), "presence-clear")
	if status != http.StatusOK && status != http.StatusAccepted || result.Result == nil {
		t.Fatalf("presence clear failed: status=%d result=%+v", status, result)
	}
	cleared := pollBearerReaction(t, f, clear.RequestID)
	if cleared.Card == nil || cleared.Card.Card.State != "unknown" || cleared.Card.Card.Note != "" || !cleared.Card.Card.ExpiresAt.IsZero() {
		t.Fatalf("presence clear acknowledgement mismatch: %+v", cleared)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		card := nativeCard(t, f.socket, "fixture-author")
		if card.State == "unknown" && card.Note == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("native presence was not cleared: %+v", card)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func nativeCard(t *testing.T, socket, nick string) irc.AgentCard {
	t.Helper()
	client := dial(t, socket, "card-reader", "", false, false)
	defer client.Close()
	if err := client.Directory(nick); err != nil {
		t.Fatal(err)
	}
	event := next(t, client, func(e *irc.DirectoryEvent) bool { return e.Nick == nick })
	next(t, client, func(e *irc.EndOfDirectoryEvent) bool { return true })
	return event.AgentCard
}
