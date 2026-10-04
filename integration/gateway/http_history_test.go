package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPHistoryPreservesReplyMetadataAndGapAndHonorsRevocation(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	post := func(id, body, parent string) *irc.MessageEvent {
		t.Helper()
		r := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: body}
		if parent != "" {
			r.Operation = core.Reply
			r.ParentID = parent
		}
		if status, _, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r)); status != 202 {
			t.Fatal("history setup post failed", status, string(data))
		}
		return next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
	}
	root := post("history-root", "root\nwhole body", "")
	reply := post("history-reply", "retained reply", root.ID)
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}}
	read := func() backend.HistoryPage {
		t.Helper()
		status, protocol, data := httpCall(t, f, http.MethodGet, "/v1/history?"+query.Encode(), f.token, nil)
		var page backend.HistoryPage
		if err := json.Unmarshal(data, &page); err != nil || status != 200 || protocol != 2 {
			t.Fatal("HTTP history failed", status, string(data), err)
		}
		return page
	}
	first := read()
	if len(first.Messages) != 1 || first.Messages[0].ID != root.ID || first.Messages[0].Message != root.Message || first.Status != "more" {
		t.Fatal("whole history body changed", first)
	}
	query.Set("cursor", first.Cursor)
	second := read()
	if len(second.Messages) != 1 || second.Messages[0].ID != reply.ID || second.Messages[0].ReplyTo != root.ID || second.Messages[0].ThreadID != root.ID || second.Status != "ok" {
		t.Fatal("reply history metadata changed", second)
	}
	var last *irc.MessageEvent
	for i := range 33 {
		last = post(fmt.Sprintf("history-evict-%d", i), fmt.Sprintf("history eviction body %d", i), "")
	}
	query.Set("cursor", first.Cursor)
	gap := read()
	if !gap.Gap || gap.Status != "more" || len(gap.Messages) != 1 {
		t.Fatal("HTTP retention gap hidden", gap)
	}
	query.Del("cursor")
	query.Set("start", "recent")
	if recent := read(); len(recent.Messages) != 1 || recent.Messages[0].ID != last.ID || recent.Status != "ok" {
		t.Fatal("recent snapshot mismatch", recent)
	}
	query.Del("start")
	query.Set("cursor", first.Cursor)
	if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/history?"+query.Encode(), f.token, nil); status != 403 {
		t.Fatal("valid cursor bypassed current revocation", status)
	}
	if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/history?"+query.Encode(), f.other, nil); status != 403 {
		t.Fatal("history bypassed operation permission", status)
	}
}

func TestHTTP1HistorySnapshot(t *testing.T) {
	f := gatewayHTTPFixture(t, false)
	r := core.Request{Version: 1, RequestID: "http1-history", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "HTTP one history"}
	if status, _, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r)); status != 202 {
		t.Fatal("HTTP1 history setup failed", status, string(data))
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == r.Message })
	status, protocol, data := httpCall(t, f, http.MethodGet, "/v1/history?kind=room&target=%23gateway-fixture&limit=1", f.token, nil)
	var page backend.HistoryPage
	if err := json.Unmarshal(data, &page); err != nil || status != 200 || protocol != 1 || len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Cursor == "" {
		t.Fatal("HTTP1 snapshot failed", status, protocol, string(data), err)
	}
}
