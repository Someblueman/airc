package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPWatchNativeWakeOpaqueScopeAndRevocation(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", http2), func(t *testing.T) {
			f := gatewayHTTPFixture(t, http2, "watch")
			const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			author := dial(t, f.socket, "fixture-author", account, false, true)
			query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"2"}}
			callCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, err := http.NewRequestWithContext(callCtx, http.MethodGet, f.server.URL+"/v1/watch?"+query.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+f.token)
			type result struct {
				status, protocol int
				page             backend.HistoryPage
				err              error
			}
			done := make(chan result, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("HTTP watch caller survived cleanup")
				}
			})
			go func() {
				defer close(finished)
				response, err := f.server.Client().Do(request)
				if err != nil {
					done <- result{err: err}
					return
				}
				defer response.Body.Close()
				var page backend.HistoryPage
				err = json.NewDecoder(response.Body).Decode(&page)
				done <- result{response.StatusCode, response.ProtoMajor, page, err}
			}()
			select {
			case <-f.watchReady:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP watch never subscribed and snapshotted")
			}
			if err := author.SendWithID("#gateway-fixture", "native live\nwhole body", "http-watch-live"); err != nil {
				t.Fatal(err)
			}
			receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == "http-watch-live" })
			observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.ID })
			var page backend.HistoryPage
			select {
			case got := <-done:
				if got.err != nil || got.status != 200 || got.protocol != 1 && !http2 || got.protocol != 2 && http2 || len(got.page.Messages) != 1 || got.page.Gap || got.page.Status != "ok" {
					t.Fatal("HTTP live watch failed", got)
				}
				page = got.page
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP watch did not wake")
			}
			m := page.Messages[0]
			if m.ID != receipt.ID || m.Message != observed.Message || m.From != observed.From || m.Target != observed.Target || m.Receipt != nil || page.Cursor == receipt.ID || page.Cursor == "" {
				t.Fatal("watch changed metadata or exposed raw cursor", page)
			}
			select {
			case <-f.watchJoined:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP watch did not join lease")
			}
			for _, test := range []struct {
				route, token string
				status       int
			}{
				{"/v1/history?kind=room&target=%23gateway-fixture&cursor=" + url.QueryEscape(page.Cursor), f.token, 400},
				{"/v1/watch?kind=room&target=%23gateway-fixture&cursor=" + receipt.ID, f.token, 400},
				{"/v1/watch?kind=room&target=%23gateway-fixture&cursor=" + url.QueryEscape(page.Cursor) + "x", f.token, 400},
				{"/v1/watch?kind=room&target=%23private", f.token, 403},
				{"/v1/watch?" + query.Encode(), f.other, 403},
				{"/v1/watch?" + query.Encode() + "&limit=3", f.token, 400},
				{"/v1/watch?" + query.Encode() + "&query=forbidden", f.token, 400},
			} {
				if status, _, data := httpCall(t, f, http.MethodGet, test.route, test.token, nil); status != test.status {
					t.Fatal("watch denial failed", status, test.status, string(data))
				}
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			if status, _, _ := httpCall(t, f, http.MethodGet, "/v1/watch?"+query.Encode(), f.token, nil); status != 403 {
				t.Fatal("watch bypassed revocation", status)
			}
		})
	}
}
