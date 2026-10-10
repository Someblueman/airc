package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPDetachedRetainedWatchPrefix(t *testing.T) {
	f := httpGatewayFixture(t, true, nil, false, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, f.socket, "fixture-author", account, false, true)
	ids := []string{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprint("signed-retained-", i)
		if err := author.SendWithID("#gateway-fixture", id, id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
		ids = append(ids, receipt.ID)
	}
	for _, encrypted := range []bool{false, true} {
		r := core.Request{Version: 1, RequestID: "retained", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1, Start: "retained"}
		// The stream first emits the earliest contiguous retained prefix, then
		// requests renewal; a newly signed cursor advances to the next prefix.
		for i := 0; i < 2; i++ {
			response, reader := detachedEvents(t, f, t.Context(), r, encrypted, 0)
			page, cursor := ssePage(t, reader)
			if response.StatusCode != 200 || len(page.Messages) != 1 || page.Messages[0].ID != ids[i] || page.Status != "more" || cursor == "" {
				t.Fatal("signed retained stream skipped prefix", encrypted, i, page, response.Status)
			}
			ended := false
			for reader.Scan() {
				if reader.Text() == `data: {"reason":"resume"}` {
					ended = true
				}
			}
			if !ended || reader.Err() != nil {
				t.Fatal("prefix stream did not end for renewal", ended, reader.Err())
			}
			_ = response.Body.Close()
			r.Start, r.Cursor = "", cursor
		}
		// The same signed selector is supported by the long-poll route.
		r.Start, r.Cursor = "retained", ""
		status, _, result := detachedCall(t, f, f.credentials.token(t, r, encrypted), encrypted)
		if status != 200 || result.Page == nil || len(result.Page.Messages) != 1 || result.Page.Messages[0].ID != ids[0] {
			t.Fatal("signed long-poll ignored retained selector", encrypted, result, status)
		}
	}
}
