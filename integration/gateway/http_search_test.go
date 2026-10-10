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

func TestHTTPNativeRoomSearchPaginationQueryScopeAndExpiry(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", http2), func(t *testing.T) {
			f := gatewayHTTPFixture(t, http2)
			post := func(id, body, parent string) *irc.MessageEvent {
				t.Helper()
				r := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: body}
				if parent != "" {
					r.Operation = core.Reply
					r.ParentID = parent
				}
				if status, _, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, r)); status != 202 {
					t.Fatal("search setup post failed", status, string(data))
				}
				return next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == body })
			}
			root := post("search-root", "Café first\nwhole body", "")
			post("search-noise", "unrelated body", "")
			reply := post("search-reply", "café second", root.ID)
			otherAuthor := dial(t, f.socket, "search-author", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", true, true)
			if err := otherAuthor.SendWithID("#gateway-fixture", "CAFÉ from another author", "observer-search"); err != nil {
				t.Fatal(err)
			}
			next(t, otherAuthor, func(e *irc.SendReceiptEvent) bool { return e.Message == "CAFÉ from another author" })
			query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "query": {"CAFÉ"}, "from": {"FiXtUrE-AuThOr"}, "limit": {"1"}}
			read := func() backend.HistoryPage {
				t.Helper()
				status, protocol, data := httpCall(t, f, http.MethodGet, "/v1/search?"+query.Encode(), f.token, nil)
				var page backend.HistoryPage
				if err := json.Unmarshal(data, &page); err != nil || status != 200 || protocol != 1 && !http2 || protocol != 2 && http2 {
					t.Fatal("native search failed", status, protocol, string(data), err)
				}
				return page
			}
			first := read()
			if len(first.Messages) != 1 || first.Messages[0].ID != root.ID || first.Messages[0].Message != root.Message || first.Status != "more" {
				t.Fatal("first search page mismatch", first)
			}
			query.Set("cursor", first.Cursor)
			second := read()
			if len(second.Messages) != 1 || second.Messages[0].ID != reply.ID || second.Messages[0].ReplyTo != root.ID || second.Status != "ok" {
				t.Fatal("second search page mismatch", second)
			}
			query.Set("cursor", second.Cursor)
			if empty := read(); len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" {
				t.Fatal("empty search lost anchored position", empty)
			}
			query.Set("query", "different")
			if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/search?"+query.Encode(), f.token, nil); status != 400 {
				t.Fatal("query cursor scope bypass", status)
			}
			query.Set("query", "CAFÉ")
			query.Set("from", "fixture-observer")
			if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/search?"+query.Encode(), f.token, nil); status != 400 {
				t.Fatal("sender cursor scope bypass", status)
			}
			query.Set("from", "fixture-author")
			if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/history?"+url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "cursor": {first.Cursor}}.Encode(), f.token, nil); status != 400 {
				t.Fatal("search cursor reused for history", status)
			}
			for i := range 33 {
				post(fmt.Sprintf("search-evict-%d", i), fmt.Sprintf("later noise %d", i), "")
			}
			query.Set("cursor", first.Cursor)
			if expired := read(); !expired.Gap || expired.Status != "expired" || len(expired.Messages) != 0 || expired.Cursor != "" {
				t.Fatal("search expiry hidden", expired)
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/search?"+query.Encode(), f.token, nil); status != 403 {
				t.Fatal("search bypassed revocation", status)
			}
		})
	}
}
