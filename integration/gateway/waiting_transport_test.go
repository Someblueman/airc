//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestWaitingReadEmptyAndPreparedAcrossTransports(t *testing.T) {
	f := newReactionTransportFixture(t)
	parent := seedReactionParent(t, f)
	before := contextCheckSnapshot(t, f)
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
	prepared := next(t, author, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	_ = prepared
	next(t, author, func(e *irc.EndOfChatEvent) bool { return true })
	if err := peer.Chat(irc.ChatRequest{Action: "prepare", Target: parent.Target, ID: parent.ID, Seconds: 60, Text: "short signal"}); err != nil {
		t.Fatal(err)
	}
	next(t, peer, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	next(t, peer, func(e *irc.EndOfChatEvent) bool { return true })
	var expected []irc.ChatEntry
	for _, call := range []struct {
		name string
		fn   func(string) (int, dispatch.DetachedResult)
	}{
		{"http", func(token string) (int, dispatch.DetachedResult) { return reactionHTTPCall(t, f, token) }},
		{"framed", func(token string) (int, dispatch.DetachedResult) {
			return reactionFramedCall(t, f.framed, token, "waiting-framed")
		}},
		{"websocket", func(token string) (int, dispatch.DetachedResult) {
			return reactionWebSocketCall(t, f.ws, token, "waiting-websocket")
		}},
	} {
		status, result := call.fn(f.cred.token(t, core.Request{Version: 1, RequestID: "waiting-" + call.name, Operation: core.WaitingRead, Target: request.Target, ParentID: parent.ID}, true))
		if status != http.StatusOK || result.Waiting == nil || len(result.Waiting.Signals) != 2 {
			t.Fatalf("%s waiting read failed: status=%d result=%+v", call.name, status, result)
		}
		if expected == nil {
			expected = result.Waiting.Signals
		} else if !sameSignals(expected, result.Waiting.Signals) {
			t.Fatalf("%s waiting snapshot differs: got=%+v want=%+v", call.name, result.Waiting.Signals, expected)
		}
	}
	waitingQuery := "/v1/waiting?kind=room&target=" + url.QueryEscape(parent.Target) + "&parent_id=" + url.QueryEscape(parent.ID)
	status, body := headerRead(t, f, waitingQuery)
	if status != http.StatusOK {
		t.Fatalf("authorSpec waiting read failed: status=%d body=%s", status, body)
	}
	var headerWaiting core.WaitingResult
	if err := json.Unmarshal(body, &headerWaiting); err != nil || !sameSignals(expected, headerWaiting.Signals) || headerWaiting.Target != *request.Target || headerWaiting.ParentID != parent.ID {
		t.Fatalf("authorSpec waiting mismatch: err=%v result=%+v expected=%+v", err, headerWaiting, expected)
	}
	checkRoomWaitingHTTP2(t, f, request, waitingQuery, headerWaiting)
	for _, bad := range []string{
		waitingQuery + "&unknown=x",
		waitingQuery + "&parent_id=other",
		"/v1/waiting?kind=direct&target=fixture-author&parent_id=" + url.QueryEscape(parent.ID),
	} {
		if status, _ := headerRead(t, f, bad); status == http.StatusOK {
			t.Fatalf("invalid waiting query accepted: %s", bad)
		}
	}
	if status, _ := headerReadToken(t, f, waitingQuery, wrongAuthorSpec); status != http.StatusUnauthorized {
		t.Fatalf("invalid waiting authorSpec status=%d, want 401", status)
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
	next(t, peer, func(e *irc.ChatEvent) bool { return e.Action == "prepare" && e.ID == parent.ID })
	next(t, peer, func(e *irc.EndOfChatEvent) bool { return true })
	_ = peer.Close()
	time.Sleep(1200 * time.Millisecond)
	afterExpiry := request
	afterExpiry.RequestID = "waiting-after-expiry"
	status, result = reactionHTTPCall(t, f, f.cred.token(t, afterExpiry, true))
	if status != http.StatusOK || result.Waiting == nil || len(result.Waiting.Signals) != 0 {
		t.Fatalf("expired signal was retained: status=%d result=%+v", status, result)
	}
	for _, target := range []*core.Target{{Kind: "room", Name: "#other-private"}, {Kind: "direct", Name: "fixture-author"}} {
		bad := request
		bad.RequestID = "waiting-deny-" + target.Kind
		bad.Target = target
		status, result := reactionHTTPCall(t, f, f.cred.token(t, bad, true))
		if status == http.StatusOK || result.Waiting != nil {
			t.Fatalf("waiting target disclosed: status=%d result=%+v", status, result)
		}
	}
	missing := request
	missing.RequestID, missing.ParentID = "waiting-missing", strings.Repeat("a", 32)
	status, result = reactionHTTPCall(t, f, f.cred.token(t, missing, true))
	if status == http.StatusOK || result.Waiting != nil {
		t.Fatalf("missing waiting parent disclosed: status=%d result=%+v", status, result)
	}
	if after := contextCheckSnapshot(t, f); !sameSnapshot(before, after) {
		t.Fatal("waiting reads mutated native history")
	}
	if status, _ := headerRead(t, f, "/v1/requests/waiting-http"); status != http.StatusNotFound {
		t.Fatalf("waiting read created delivery receipt: status=%d", status)
	}
}
