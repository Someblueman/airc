//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestConversationSelectors(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	root, direct, nested, reaction, unrelated := seedConversationThreads(t, f)
	before := contextCheckSnapshot(t, f)

	for _, mode := range []string{"replies", "thread"} {
		r := core.Request{Version: 1, RequestID: "conversation-" + mode, Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: direct.ID, ThreadMode: mode, Limit: 50}
		status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
		if status != http.StatusOK || result.Page == nil {
			t.Fatalf("%s selector failed: status=%d result=%+v", mode, status, result)
		}
		ids := pageIDs(result.Page)
		if mode == "replies" && (ids[reaction.ID] || ids[root.ID] || !ids[nested.ID] || ids[unrelated.ID]) {
			t.Fatalf("direct replies selector leaked records: %+v", ids)
		}
		if mode == "thread" && (!ids[root.ID] || !ids[direct.ID] || !ids[nested.ID] || !ids[reaction.ID] || ids[unrelated.ID]) {
			t.Fatalf("thread selector mismatch: %+v", ids)
		}
	}
	if after := contextCheckSnapshot(t, f); !sameSnapshot(before, after) {
		t.Fatalf("conversation reads mutated native history: before=%v after=%v", before, after)
	}
}

func TestConversationCursorBindsModeParentRoomAndOperation(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	_, direct, _, _, _ := seedConversationThreads(t, f)
	r := core.Request{Version: 1, RequestID: "conversation-cursor", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: direct.ID, ThreadMode: "thread", Limit: 1}
	status, first := reactionHTTPCall(t, f, f.cred.token(t, r, true))
	if status != http.StatusOK || first.Page == nil || first.Page.Cursor == "" {
		t.Fatalf("thread cursor was not issued: status=%d page=%+v", status, first.Page)
	}
	for _, mutate := range []func(*core.Request){
		func(q *core.Request) { q.ThreadMode = "replies" },
		func(q *core.Request) { q.ParentID = strings.Repeat("a", 32) },
		func(q *core.Request) { q.Target = &core.Target{Kind: "room", Name: "#other-private"} },
		func(q *core.Request) { q.Operation = core.Search; q.Query = "thread" },
	} {
		q := r
		q.RequestID = "conversation-cursor-invalid"
		q.Cursor = first.Page.Cursor
		mutate(&q)
		status, _ := reactionHTTPCall(t, f, f.cred.token(t, q, true))
		if status == http.StatusOK {
			t.Fatalf("cursor crossed selector scope: status=%d request=%+v", status, q)
		}
	}
}

func TestConversationSelectorDenials(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	root, direct, _, _, _ := seedConversationThreads(t, f)
	foreign := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	if err := foreign.SendWithID("#other-private", "foreign thread trigger", "conversation-foreign"); err != nil {
		t.Fatal(err)
	}
	foreignReceipt := next(t, foreign, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-foreign" })
	_ = foreign.Close()
	for _, tc := range []struct {
		name   string
		parent string
		target *core.Target
		want   int
	}{
		{"wrong-room", foreignReceipt.ID, &core.Target{Kind: "room", Name: "#gateway-fixture"}, http.StatusForbidden},
		{"missing", strings.Repeat("b", 32), &core.Target{Kind: "room", Name: "#gateway-fixture"}, http.StatusForbidden},
		{"direct", root.ID, &core.Target{Kind: "direct", Name: "fixture-author"}, http.StatusBadRequest},
	} {
		r := core.Request{Version: 1, RequestID: "conversation-deny-" + tc.name, Operation: core.HistoryRead, Target: tc.target, ParentID: tc.parent, ThreadMode: "thread", Limit: 20}
		status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
		if status != tc.want || result.Page != nil {
			t.Fatalf("%s selector disclosure/status: status=%d want=%d result=%+v", tc.name, status, tc.want, result)
		}
	}
	_ = direct
}

func TestConversationSearchSelectorFiltersSameRoomThreads(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	_, direct, nested, _, unrelated := seedConversationThreads(t, f)
	r := core.Request{Version: 1, RequestID: "conversation-search-thread", Operation: core.Search, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: direct.ID, ThreadMode: "thread", Query: "thread", Limit: 50}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, r, true))
	if status != http.StatusOK || result.Page == nil {
		t.Fatalf("thread search failed: status=%d result=%+v", status, result)
	}
	ids := pageIDs(result.Page)
	if !ids[direct.ID] || !ids[nested.ID] || ids[unrelated.ID] {
		t.Fatalf("thread search omitted selected records: %+v", ids)
	}
}

func TestConversationRepliesWatchHTTPAndWebSocketLiveCancellation(t *testing.T) {
	t.Parallel()
	f := newReactionTransportFixture(t)
	_, direct, nested, _, _ := seedConversationThreads(t, f)
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"20"}, "parent_id": {direct.ID}, "thread_mode": {"replies"}}
	callCtx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, f.httpURL+"/v1/events?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, f.authorSpec)
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("thread SSE status=%d", response.StatusCode)
	}
	reader := bufio.NewScanner(response.Body)
	reader.Buffer(make([]byte, 4096), backend.MaxHistoryBytes)
	initial, _ := ssePage(t, reader)
	if len(initial.Messages) != 1 || initial.Messages[0].ReplyTo != direct.ID || initial.Messages[0].Reaction != "" {
		t.Fatalf("thread SSE initial selector leaked records: %+v", initial)
	}

	watch := core.Request{Version: 1, RequestID: "conversation-ws-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: direct.ID, ThreadMode: "replies", Limit: 20, Start: "recent"}
	websocketWrite(t, f.watch, frameRequest{Version: 1, CorrelationID: "conversation-ws-watch", Profile: "plain", Request: f.cred.token(t, watch, true), Stream: true})
	wsInitial := websocketRead(t, f.watch)
	if wsInitial.Event != "page" || wsInitial.Page == nil || len(wsInitial.Page.Messages) != 1 || wsInitial.Page.Messages[0].ReplyTo != direct.ID {
		t.Fatalf("thread WebSocket initial selector leaked records: %+v", wsInitial)
	}

	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	if err := author.React(direct.ID, "checking"); err != nil {
		t.Fatal(err)
	}
	_ = next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Reaction == "checking" && e.ReplyTo == direct.ID })
	if err := author.ReplyWithID(nested.ID, "excluded deeper nested", "conversation-live-excluded"); err != nil {
		t.Fatal(err)
	}
	_ = next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-live-excluded" })
	if err := author.ReplyWithID(direct.ID, "selected live nested", "conversation-live-selected"); err != nil {
		t.Fatal(err)
	}
	selectedReceipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-live-selected" })
	page, _ := ssePage(t, reader)
	if len(page.Messages) != 1 || page.Messages[0].ID != selectedReceipt.ID {
		t.Fatalf("thread SSE live selector mismatch: %+v", page)
	}
	wsLive := websocketRead(t, f.watch)
	if wsLive.Event != "page" || wsLive.Page == nil || len(wsLive.Page.Messages) != 1 || wsLive.Page.Messages[0].ID != selectedReceipt.ID {
		t.Fatalf("thread WebSocket live selector mismatch: %+v", wsLive)
	}
	cancel()
	_ = response.Body.Close()
	if reader.Scan() {
		t.Fatalf("cancelled SSE returned another record: %q", reader.Text())
	}
	_ = author.Close()
}

func TestConversationThreadWatchWebSocketLiveSelection(t *testing.T) {
	t.Parallel()
	f := newReactionTransportFixture(t)
	root, direct, _, _, _ := seedConversationThreads(t, f)
	watch := core.Request{Version: 1, RequestID: "conversation-thread-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: direct.ID, ThreadMode: "thread", Limit: 20, Start: "recent"}
	websocketWrite(t, f.watch, frameRequest{Version: 1, CorrelationID: "conversation-thread-watch", Profile: "plain", Request: f.cred.token(t, watch, true), Stream: true})
	initial := websocketRead(t, f.watch)
	if initial.Event != "page" || initial.Page == nil || !pageIDs(initial.Page)[root.ID] {
		t.Fatalf("thread watch did not acknowledge initial snapshot: %+v", initial)
	}
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	if err := author.SendWithID("#gateway-fixture", "unrelated live root", "conversation-thread-unrelated"); err != nil {
		t.Fatal(err)
	}
	_ = next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-thread-unrelated" })
	if err := author.React(root.ID, "checking"); err != nil {
		t.Fatal(err)
	}
	selected := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Reaction == "checking" && e.ReplyTo == root.ID })
	page := websocketRead(t, f.watch)
	if page.Event != "page" || page.Page == nil || len(page.Page.Messages) != 1 || page.Page.Messages[0].ID != selected.ID {
		t.Fatalf("thread watch leaked unrelated live traffic: %+v", page)
	}
	_ = author.Close()
	_ = f.watch.CloseNow()
}

func TestConversationThreadRetainsDescendantAfterRootEviction(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	root, direct, nested, _ := seedRetentionChain(t, f, false)
	beforeRequest := core.Request{Version: 1, RequestID: "conversation-root-before-eviction", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: root.ID, ThreadMode: "thread", Limit: 1}
	beforeStatus, beforeResult := settledHTTPRead(t, f, beforeRequest, evictionReadWithin)
	if beforeStatus != http.StatusOK || beforeResult.Page == nil || beforeResult.Page.Cursor == "" {
		t.Fatalf("failed to capture pre-eviction thread cursor: status=%d result=%+v", beforeStatus, beforeResult)
	}
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for i := 0; i < 30; i++ {
		id := "conversation-root-evict-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if err := author.SendWithID("#gateway-fixture", "root eviction filler "+id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	_ = author.Close()
	// Each receipt arrived after its message was in the window, in the
	// history file and in the file's in-memory index, so the root is
	// evicted and the archive holds the cursor before the reads below; only
	// the exchange with the gateway can still be slow on a loaded host.
	r := core.Request{Version: 1, RequestID: "conversation-root-evicted", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: root.ID, ThreadMode: "thread", Limit: 50}
	status, result := settledHTTPRead(t, f, r, evictionReadWithin)
	if status != http.StatusOK || result.Page == nil {
		t.Fatalf("evicted-root thread read failed: status=%d result=%+v", status, result)
	}
	ids := pageIDs(result.Page)
	if ids[root.ID] || !ids[direct.ID] || !ids[nested.ID] || result.Page.Gap {
		t.Fatalf("evicted-root thread lost descendant or gap: page=%+v", result.Page)
	}
	resume := r
	resume.RequestID = "conversation-root-evicted-resume"
	resume.Cursor = beforeResult.Page.Cursor
	// The history file's archive still holds the cursor, so the thread
	// resumes from it without a gap.
	status, result = settledHTTPRead(t, f, resume, evictionReadWithin)
	if status != http.StatusOK || result.Page == nil || result.Page.Gap {
		t.Fatalf("evicted-root cursor did not resume from the archive: status=%d page=%+v", status, result.Page)
	}
	if ids := pageIDs(result.Page); !ids[direct.ID] || !ids[nested.ID] {
		t.Fatalf("resumed thread lost a descendant: page=%+v", result.Page)
	}
}

func TestConversationRepliesRetainsChildrenOfEvictedParent(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	_, _, nested, child := seedRetentionChain(t, f, true)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	for i := 0; i < 30; i++ {
		id := "conversation-parent-evict-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if err := author.SendWithID("#gateway-fixture", "parent eviction filler "+id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	_ = author.Close()
	r := core.Request{Version: 1, RequestID: "conversation-parent-evicted", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: nested.ID, ThreadMode: "replies", Limit: 50}
	status, result := settledHTTPRead(t, f, r, evictionReadWithin)
	if status != http.StatusOK || result.Page == nil {
		t.Fatalf("evicted-parent replies read failed: status=%d result=%+v", status, result)
	}
	ids := pageIDs(result.Page)
	if !ids[child.ID] || ids[nested.ID] || result.Page.Messages[0].Reaction != "" {
		t.Fatalf("evicted-parent replies selector mismatch: page=%+v", result.Page)
	}
}

func TestConversationRepliesEmptyAndForeignChildDenial(t *testing.T) {
	t.Parallel()
	f := newReactionHTTPFixture(t)
	_, _, nested, _ := seedRetentionChain(t, f, false)
	empty := core.Request{Version: 1, RequestID: "conversation-empty-replies", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: nested.ID, ThreadMode: "replies", Limit: 50}
	status, result := reactionHTTPCall(t, f, f.cred.token(t, empty, true))
	if status != http.StatusOK || result.Page == nil || len(result.Page.Messages) != 0 {
		t.Fatalf("empty replies read mismatch: status=%d result=%+v", status, result)
	}
	foreign := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	if err := foreign.SendWithID("#other-private", "foreign parent", "conversation-foreign-parent"); err != nil {
		t.Fatal(err)
	}
	parent := next(t, foreign, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-foreign-parent" })
	if err := foreign.ReplyWithID(parent.ID, "foreign child", "conversation-foreign-child"); err != nil {
		t.Fatal(err)
	}
	child := next(t, foreign, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-foreign-child" })
	_ = foreign.Close()
	for _, id := range []string{parent.ID, child.ID} {
		r := core.Request{Version: 1, RequestID: "conversation-foreign-child-read-" + id[:6], Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, ParentID: id, ThreadMode: "replies", Limit: 50}
		status, result = reactionHTTPCall(t, f, f.cred.token(t, r, true))
		if status == http.StatusOK || result.Page != nil {
			t.Fatalf("foreign child disclosed: status=%d result=%+v", status, result)
		}
	}
}

func seedRetentionChain(t *testing.T, f *reactionTransportFixture, child bool) (*irc.MessageEvent, *irc.MessageEvent, *irc.MessageEvent, *irc.MessageEvent) {
	t.Helper()
	root := seedReactionParent(t, f)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	post := func(parent, body, id string) *irc.MessageEvent {
		if err := author.ReplyWithID(parent, body, id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
		return next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	}
	direct := post(root.ID, "retention direct", "conversation-retention-direct")
	nested := post(direct.ID, "retention nested", "conversation-retention-nested")
	var childMessage *irc.MessageEvent
	if child {
		childMessage = post(nested.ID, "retention child", "conversation-retention-child")
	}
	return root, direct, nested, childMessage
}

func seedConversationThreads(t *testing.T, f *reactionTransportFixture) (*irc.MessageEvent, *irc.MessageEvent, *irc.MessageEvent, *irc.MessageEvent, *irc.MessageEvent) {
	t.Helper()
	root := seedReactionParent(t, f)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	postReply := func(parent, body, id string) *irc.MessageEvent {
		if err := author.ReplyWithID(parent, body, id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
		return next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
	}
	direct := postReply(root.ID, "direct thread reply", "conversation-direct")
	nested := postReply(direct.ID, "nested thread reply", "conversation-nested")
	if err := author.React(root.ID, "agree"); err != nil {
		t.Fatal(err)
	}
	reactionReceipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Reaction == "agree" && e.ReplyTo == root.ID })
	reaction := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == reactionReceipt.ID })
	if err := author.SendWithID("#gateway-fixture", "same room unrelated thread root", "conversation-unrelated-root"); err != nil {
		t.Fatal(err)
	}
	unrelatedReceipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "conversation-unrelated-root" })
	unrelatedRoot := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == unrelatedReceipt.ID })
	return root, direct, nested, reaction, unrelatedRoot
}

func pageIDs(page *backend.HistoryPage) map[string]bool {
	ids := make(map[string]bool)
	for _, message := range page.Messages {
		ids[message.ID] = true
	}
	return ids
}

func sameSnapshot(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, body := range a {
		if b[id] != body {
			return false
		}
	}
	return true
}
