//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
)

func admitHeaderCard(t *testing.T, f *reactionTransportFixture, request core.Request) core.Result {
	t.Helper()
	admitMutationHTTP(t, f, http.MethodPost, "/v1/messages", request)
	return admitHeaderCardResult(t, f, request)
}

// admitHeaderCardResult waits for an admitted card mutation's acceptance.
func admitHeaderCardResult(t *testing.T, f *reactionTransportFixture, request core.Request) core.Result {
	t.Helper()
	accepted := pollHeaderReaction(t, f, request.RequestID)
	if accepted.Card == nil || accepted.Accepted == nil || !*accepted.Accepted || accepted.MessageID != "" || accepted.Persisted != nil || accepted.RecipientConnected != nil || accepted.LookupAttempts != 0 {
		t.Fatalf("card outcome has missing or invented facts: %+v", accepted)
	}
	return accepted
}

func TestCardEscapedNativeProfileAndClear(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	value := strings.Repeat("<", 200)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for _, field := range []string{"model", "workspace", "tools", "about"} {
		if err := author.UpdateProfile(map[string]string{field: value}); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.DirectoryEvent) bool { return e.Nick == "fixture-author" })
		next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
	}
	if err := author.Close(); err != nil {
		t.Fatal(err)
	}
	result := admitHeaderCard(t, f, core.Request{Version: 1, RequestID: "escaped-card", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Model: &value}})
	encoded, err := json.Marshal(result.Card)
	if err != nil || len(encoded) <= 4096 || len(encoded) > core.MaxCardAcknowledgementBytes {
		t.Fatalf("escaped native card: bytes=%d err=%v", len(encoded), err)
	}
	card := result.Card.Card
	if card.Model != value || card.Workspace != value || card.Tools != value || card.About != value {
		t.Fatalf("lost existing profile fields: %+v", card)
	}
	observed := nativeCard(t, f.socket, "fixture-author")
	if observed.AgentProfile != card.AgentProfile || observed.LastSeen.Before(card.LastSeen) || observed.AccountID != card.AccountID {
		t.Fatalf("native profile differs: observed=%+v ack=%+v", observed, card)
	}
	empty := ""
	cleared := admitHeaderCard(t, f, core.Request{Version: 1, RequestID: "clear-field", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: &empty}})
	if cleared.Card.Card.About != "" || cleared.Card.Card.Model != value || nativeCard(t, f.socket, "fixture-author").About != "" {
		t.Fatal("explicit empty field did not clear only that field")
	}
	all := admitHeaderCard(t, f, core.Request{Version: 1, RequestID: "clear-profile", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{Clear: true}})
	if all.Card.Card.AgentProfile != (irc.AgentProfile{}) || nativeCard(t, f.socket, "fixture-author").AgentProfile != (irc.AgentProfile{}) {
		t.Fatal("full profile clear did not reach native state")
	}
}

func TestPresenceExpiresWhileStoredAcknowledgementRemains(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	request := core.Request{Version: 1, RequestID: "expiry-card", Operation: core.PresenceSet, Presence: &core.PresenceInput{State: "running", Note: "  expiring  ", TTLSeconds: 1}}
	accepted := admitHeaderCard(t, f, request)
	card := accepted.Card.Card
	if card.State != "running" || card.Note != "expiring" || card.ExpiresAt.Sub(card.LastSeen) != time.Second {
		t.Fatalf("presence native timing: %+v", card)
	}
	var observed irc.AgentCard
	if !testwait.Poll(5*time.Second, 100*time.Millisecond, func() bool {
		observed = nativeCard(t, f.socket, "fixture-author")
		return observed.State == "unknown" && observed.Note == ""
	}) {
		t.Fatalf("presence did not expire natively: %+v", observed)
	}
	status, body := headerRead(t, f, "/v1/requests/"+request.RequestID)
	var saved core.Result
	if status != http.StatusOK || json.Unmarshal(body, &saved) != nil || saved.Card == nil || !reflect.DeepEqual(saved.Card.Card, card) || saved.SendAttempts != 1 {
		t.Fatalf("stored acknowledgement changed after native expiry: status=%d body=%s", status, body)
	}
}

func TestNativeOversizedCardAcknowledgementRemainsUncertain(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	value := strings.Repeat("<", 400)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for _, field := range []string{"model", "workspace", "tools"} {
		if err := author.UpdateProfile(map[string]string{field: value}); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.DirectoryEvent) bool { return e.Nick == "fixture-author" })
		next(t, author, func(e *irc.EndOfDirectoryEvent) bool { return true })
	}
	author.Close()
	policy := core.ReceiptOnly
	expiry := time.Now().Unix() + 4
	request := core.Request{Version: 1, RequestID: "native-card-line-limit", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: &value}, DeliveryPolicy: &policy, ExpiresAt: &expiry}
	status, result, _ := headerReactionHTTP(t, f, http.MethodPost, "/v1/messages", requestJSON(t, request))
	if status != http.StatusAccepted || !result.Durable {
		t.Fatalf("legal native patch not admitted: status=%d result=%+v", status, result)
	}
	var receipt core.Result
	if !testwait.Poll(8*time.Second, 50*time.Millisecond, func() bool {
		status, body := headerRead(t, f, "/v1/requests/"+request.RequestID)
		receipt = core.Result{}
		if status != http.StatusOK || json.Unmarshal(body, &receipt) != nil {
			t.Fatalf("line-limit receipt unavailable: status=%d body=%s", status, body)
		}
		return receipt.AcceptanceUncertain
	}) {
		t.Fatalf("native oversized ACK did not retain uncertainty: %+v", receipt)
	}
	if receipt.Card != nil || receipt.Accepted != nil || receipt.Persisted != nil || receipt.MessageID != "" || receipt.SendAttempts != 1 || receipt.LookupAttempts != 0 {
		t.Fatalf("native missing ACK fabricated acceptance: %+v", receipt)
	}
	// Clearing just the fourth field restores a representable native card. The
	// preserved first three fields demonstrate that the server remains usable.
	restored := admitHeaderCard(t, f, core.Request{Version: 1, RequestID: "restore-native-card-line", Operation: core.ProfileUpdate, Profile: &core.ProfilePatch{About: new(string)}})
	if restored.Card.Card.Model != value || restored.Card.Card.Workspace != value || restored.Card.Card.Tools != value || restored.Card.Card.About != "" {
		t.Fatalf("native card recovery lost stored profile: %+v", restored.Card)
	}
}
