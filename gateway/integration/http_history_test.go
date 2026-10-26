package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTP2HistoryPagesRecent(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	post := func(id, body, parent string) *irc.MessageEvent {
		t.Helper()
		r := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: body}
		if parent != "" {
			r.Operation = core.Reply
			r.ParentID = parent
		}
		status, protocol, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r))
		var admitted core.Result
		if err := json.Unmarshal(data, &admitted); err != nil || status != 202 || protocol != 2 || !admitted.Durable {
			t.Fatal("HTTP2 durable admission failed", status, protocol, string(data), err)
		}
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
		if seen.From != "fixture-author" || seen.Target != r.Target.Name {
			t.Fatal("HTTP2 observer identity mismatch", seen)
		}
		return seen
	}
	root := post("history-root", "root\nwhole body 🙂", "")
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
	if len(first.Messages) != 1 || first.Messages[0].ID != root.ID || first.Messages[0].Message != root.Message || first.Messages[0].From != root.From || first.Messages[0].Target != root.Target || first.Status != "more" || first.Cursor == "" || first.Gap {
		t.Fatal("whole history body changed", first)
	}
	query.Set("cursor", first.Cursor)
	second := read()
	if len(second.Messages) != 1 || second.Messages[0].ID != reply.ID || second.Messages[0].Message != reply.Message || second.Messages[0].From != reply.From || second.Messages[0].Target != reply.Target || second.Messages[0].ReplyTo != root.ID || second.Messages[0].ThreadID != root.ID || second.Status != "ok" || second.Cursor == "" || second.Gap {
		t.Fatal("reply history metadata changed", second)
	}
	query.Set("cursor", second.Cursor)
	caughtUp := read()
	if len(caughtUp.Messages) != 0 || caughtUp.Status != "ok" || caughtUp.Cursor == "" || caughtUp.Gap {
		t.Fatal("HTTP caught-up page mismatch", caughtUp)
	}
	query.Set("cursor", caughtUp.Cursor)
	if resumed := read(); len(resumed.Messages) != 0 || resumed.Status != "ok" || resumed.Cursor == "" || resumed.Gap {
		t.Fatal("HTTP caught-up cursor moved its position", resumed)
	}
	query.Del("cursor")
	query.Set("start", "recent")
	if recent := read(); len(recent.Messages) != 1 || recent.Messages[0].ID != reply.ID || recent.Status != "ok" {
		t.Fatal("recent snapshot mismatch", recent)
	}
	query.Del("start")
	query.Set("cursor", first.Cursor)
	// The cursor is bound to its author: another author cannot resume it.
	if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/history?"+query.Encode(), f.other, nil); status != 400 {
		t.Fatal("another author resumed the cursor", status)
	}
}
