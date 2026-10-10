package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPWatchQuietPollAndRetainedResume(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}}
	read := func() backend.HistoryPage {
		t.Helper()
		status, _, data := httpCall(t, f, http.MethodGet, "/v1/watch?"+query.Encode(), f.token, nil)
		var page backend.HistoryPage
		if err := json.Unmarshal(data, &page); err != nil || status != 200 {
			t.Fatal("quiet watch failed", status, string(data), err)
		}
		return page
	}
	if empty := read(); len(empty.Messages) != 0 || empty.Cursor != "" || empty.Gap || empty.Status != "ok" {
		t.Fatal("empty archive invented progress", empty)
	}
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, f.socket, "fixture-author", account, false, true)
	for _, id := range []string{"resume-one", "resume-two"} {
		if err := author.SendWithID("#gateway-fixture", id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	query.Set("start", "retained")
	first := read()
	if len(first.Messages) != 1 || first.Messages[0].Message != "resume-one" || first.Status != "more" {
		t.Fatal("watch skipped retained prefix", first)
	}
	query.Del("start")
	query.Set("cursor", first.Cursor)
	second := read()
	if len(second.Messages) != 1 || second.Messages[0].Message != "resume-two" || second.Status != "ok" {
		t.Fatal("watch resume skipped or repeated", second)
	}
	query.Set("cursor", second.Cursor)
	if empty := read(); len(empty.Messages) != 0 || empty.Cursor == "" || empty.Status != "ok" || empty.Gap {
		t.Fatal("quiet resume lost position", empty)
	}
	for i := range 33 {
		id := fmt.Sprintf("watch-evict-%d", i)
		if err := author.SendWithID("#gateway-fixture", id, id); err != nil {
			t.Fatal(err)
		}
		next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
	}
	query.Set("cursor", first.Cursor)
	if expired := read(); !expired.Gap || len(expired.Messages) != 1 || expired.Status != "more" || expired.Cursor == "" {
		t.Fatal("watch hid retention gap", expired)
	}
}
