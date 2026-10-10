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

func TestHTTPGetPollingResumesAfterCaughtUp(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run(fmt.Sprint(protocol), func(t *testing.T) {
			f := gatewayHTTPFixture(t, protocol == 2)
			query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}}
			read := func() backend.HistoryPage {
				t.Helper()
				status, gotProtocol, data := httpCall(t, f, http.MethodGet, "/v1/history?"+query.Encode(), f.token, nil)
				var page backend.HistoryPage
				if err := json.Unmarshal(data, &page); err != nil || status != 200 || gotProtocol != protocol || page.Cursor == "" || page.Gap {
					t.Fatal("GET polling failed", status, gotProtocol, string(data), err)
				}
				query.Set("cursor", page.Cursor)
				return page
			}
			seed := core.Request{Version: 1, RequestID: "poll-seed", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "polling cursor seed"}
			status, gotProtocol, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, seed))
			if status != 202 || gotProtocol != protocol {
				t.Fatal("polling seed admission failed", status, gotProtocol, string(data))
			}
			seedNative := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == seed.Message })
			seedPage := read()
			if len(seedPage.Messages) != 1 || seedPage.Messages[0].ID != seedNative.ID || seedPage.Status != "ok" {
				t.Fatal("polling seed page mismatch", seedPage)
			}
			for range 2 {
				if page := read(); len(page.Messages) != 0 || page.Status != "ok" {
					t.Fatal("empty polling cursor mismatch", page)
				}
			}
			var observed []*irc.MessageEvent
			for _, id := range []string{"poll-first", "poll-second"} {
				request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: id + "\nwhole body 🙂"}
				status, gotProtocol, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, request))
				var admitted core.Result
				if err := json.Unmarshal(data, &admitted); err != nil || status != 202 || gotProtocol != protocol || !admitted.Durable {
					t.Fatal("polling send admission failed", status, gotProtocol, string(data), err)
				}
				seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == request.Message })
				if seen.From != "fixture-author" || seen.Target != request.Target.Name {
					t.Fatal("polling observer mismatch", seen)
				}
				observed = append(observed, seen)
			}
			for i, seen := range observed {
				page := read()
				want := "more"
				if i == len(observed)-1 {
					want = "ok"
				}
				if len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Status != want {
					t.Fatal("polling lost new native record", page)
				}
			}
			for range 2 {
				if page := read(); len(page.Messages) != 0 || page.Status != "ok" {
					t.Fatal("polling replayed caught-up records", page)
				}
			}
		})
	}
}
