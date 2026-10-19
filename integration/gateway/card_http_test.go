//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// TestProfileAndPresenceHTTP maps profile.update and presence.set to the
// author's native directory card: each saved acknowledgement is the native
// card, carries no message facts, and presence clear empties the card state.
func TestProfileAndPresenceHTTP(t *testing.T) {
	f := newReactionHTTPFixture(t)
	model, workspace, tools, about := "card-model", "card-workspace", "card-tools", "profile 🙂"
	profile := core.Request{Version: 1, RequestID: "card-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &model, Workspace: &workspace, Tools: &tools, About: &about}}
	admitted := admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", profile)
	if admitted.Card != nil && admitted.State != core.Accepted {
		t.Fatalf("profile admission invented an acknowledgement: %+v", admitted)
	}
	card := admitHeaderCardResult(t, f, profile).Card.Card
	// An admission that waited for delivery answers with the saved card.
	if admitted.Card != nil && !reflect.DeepEqual(admitted.Card.Card, card) {
		t.Fatalf("admission card differs from the saved one: %+v %+v", admitted.Card.Card, card)
	}
	if card.Model != model || card.Workspace != workspace || card.Tools != tools || card.About != about || card.Nick != "fixture-author" || card.AccountID == "" {
		t.Fatalf("profile acknowledgement lost fields: %+v", card)
	}
	if native := nativeCard(t, f.socket, "fixture-author"); native.AgentProfile != card.AgentProfile || native.AccountID != card.AccountID {
		t.Fatalf("native card differs from profile acknowledgement: native=%+v ack=%+v", native, card)
	}
	presence := core.Request{Version: 1, RequestID: "card-presence", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "thinking", Note: "presence note 🙂", TTLSeconds: 60}}
	admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", presence)
	state := admitHeaderCardResult(t, f, presence).Card.Card
	if state.State != "thinking" || state.Note != "presence note 🙂" || state.ExpiresAt.Sub(state.LastSeen) != 60*time.Second || state.AccountID != card.AccountID {
		t.Fatalf("presence acknowledgement mismatch: %+v", state)
	}
	if native := nativeCard(t, f.socket, "fixture-author"); native.State != state.State || native.Note != state.Note || !native.ExpiresAt.Equal(state.ExpiresAt) || native.AccountID != card.AccountID {
		t.Fatalf("native presence differs: native=%+v ack=%+v", native, state)
	}
	clear := core.Request{Version: 1, RequestID: "card-presence-clear", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "clear"}}
	if status, result := reactionHTTPCall(t, f, f.cred.token(t, clear, true)); status != http.StatusOK && status != http.StatusAccepted || result.Result == nil {
		t.Fatalf("presence clear admission: status=%d result=%+v", status, result)
	}
	cleared := admitHeaderCardResult(t, f, clear).Card.Card
	if cleared.State != "unknown" || cleared.Note != "" || !cleared.ExpiresAt.IsZero() {
		t.Fatalf("presence clear acknowledgement was not empty: %+v", cleared)
	}
	if native := nativeCard(t, f.socket, "fixture-author"); native.State != "unknown" || native.Note != "" {
		t.Fatalf("native presence was not cleared: %+v", native)
	}
}
