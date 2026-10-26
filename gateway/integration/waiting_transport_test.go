//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// TestWaitingReadEmptyPreparedCancelledAndExpired maps waiting.read to the
// parent's native reply signals: an explicit empty list, every actor's live
// preparation, and neither a cancelled nor an expired signal.
func TestWaitingReadEmptyPreparedCancelledAndExpired(t *testing.T) {
	f := newReactionHTTPFixture(t)
	parent := seedReactionParent(t, f)
	request := core.Request{Version: 1, RequestID: "waiting-empty", Operation: core.WaitingRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: parent.ID}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, request, true))
	if status != http.StatusOK || result.Waiting == nil || result.Waiting.Signals == nil || len(result.Waiting.Signals) != 0 {
		t.Fatalf("empty waiting mismatch: status=%d result=%+v", status, result)
	}
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	peer := dial(t, f.socket, "fixture-peer", "", false, true)
	if err := author.Chat(irc.ChatRequest{Action: "prepare", Target: parent.Target, ID: parent.ID, Seconds: 60, Text: "working on it"}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := peer.Chat(irc.ChatRequest{Action: "prepare", Target: parent.Target, ID: parent.ID, Seconds: 60, Text: "short signal"}); err != nil {
		t.Fatal(err)
	}
	next(t, peer, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	next(t, peer, func(e *irc.EndOfChatEvent) bool { return true })
	status, result = reactionHTTPCall(t, f, f.cred.token(t, core.Request{Version: 1, RequestID: "waiting-prepared", Operation: core.WaitingRead, Target: request.Target, ParentID: parent.ID}, true))
	if status != http.StatusOK || result.Waiting == nil || len(result.Waiting.Signals) != 2 {
		t.Fatalf("prepared waiting read failed: status=%d result=%+v", status, result)
	}
	status, body := headerRead(t, f, "/v1/waiting?kind=room&target="+url.QueryEscape(parent.Target)+"&parent_id="+url.QueryEscape(parent.ID))
	var headerWaiting core.WaitingResult
	if err := json.Unmarshal(body, &headerWaiting); status != http.StatusOK || err != nil || !sameSignals(result.Waiting.Signals, headerWaiting.Signals) || headerWaiting.Target != *request.Target || headerWaiting.ParentID != parent.ID {
		t.Fatalf("GET waiting mismatch: status=%d body=%s expected=%+v", status, body, result.Waiting)
	}
	// Cancel the long-lived actor signal and verify the snapshot changes without
	// involving a gateway mutation or delivery receipt.
	if err := author.Chat(irc.ChatRequest{Action: "cancel", Target: parent.Target, ID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "cancel" && e.ID == parent.ID })
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	_ = author.Close()
	cancelled := request
	cancelled.RequestID = "waiting-after-cancel"
	status, result = reactionHTTPCall(t, f, f.cred.token(t, cancelled, true))
	if status != http.StatusOK || result.Waiting == nil || len(result.Waiting.Signals) != 1 {
		t.Fatalf("cancelled signal remained: status=%d result=%+v", status, result)
	}
	// Native signals throttle updates for two seconds. Cancel the existing
	// signal before creating a new one with a short expiry.
	if err := peer.Chat(irc.ChatRequest{Action: "cancel", ID: parent.ID}); err != nil {
		t.Fatal(err)
	}
	next(t, peer, func(e *irc.ChatEvent) bool { return e.Action == "cancel" && e.ID == parent.ID })
	next(t, peer, func(e *irc.EndOfChatEvent) bool { return true })
	if err := peer.Chat(irc.ChatRequest{Action: "prepare", Target: parent.Target, ID: parent.ID, Seconds: 1, Text: "short signal"}); err != nil {
		t.Fatal(err)
	}
	short := next(t, peer, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	next(t, peer, func(e *irc.EndOfChatEvent) bool { return true })
	_ = peer.Close()
	// Native signals end at their expiry; read once it has passed.
	time.Sleep(time.Until(short.ExpiresAt))
	afterExpiry := request
	afterExpiry.RequestID = "waiting-after-expiry"
	status, result = reactionHTTPCall(t, f, f.cred.token(t, afterExpiry, true))
	if status != http.StatusOK || result.Waiting == nil || len(result.Waiting.Signals) != 0 {
		t.Fatalf("expired signal was retained: status=%d result=%+v", status, result)
	}
	missing := request
	missing.RequestID, missing.ParentID = "waiting-missing", strings.Repeat("a", 32)
	status, result = reactionHTTPCall(t, f, f.cred.token(t, missing, true))
	if status == http.StatusOK || result.Waiting != nil {
		t.Fatalf("missing waiting parent disclosed: status=%d result=%+v", status, result)
	}
}
