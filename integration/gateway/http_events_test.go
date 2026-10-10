package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/pkg/irc"
)

func ssePage(t *testing.T, reader *bufio.Scanner) (backend.HistoryPage, string) {
	t.Helper()
	var page backend.HistoryPage
	id, data, event := "", "", ""
	for reader.Scan() {
		line := reader.Text()
		if line == "" {
			if event == "page" && data != "" {
				if err := json.Unmarshal([]byte(data), &page); err != nil {
					t.Fatal(err)
				}
				return page, id
			}
			id, data, event = "", "", ""
			continue
		}
		if strings.HasPrefix(line, "id: ") {
			id = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatal("stream ended before page", reader.Err())
	return page, id
}

func TestHTTPEventsSameLeaseLiveResumeAndRevocation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			f := gatewayHTTPFixture(t, h2, "watch")
			const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			author := dial(t, f.socket, "fixture-author", account, false, true)
			query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"2"}}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/v1/events?"+query.Encode(), nil)
			req.Header.Set("Authorization", "Bearer "+f.token)
			response, err := f.server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.ProtoMajor != 1 && !h2 || response.ProtoMajor != 2 && h2 {
				t.Fatal("SSE negotiation failed", response.Status, response.Proto)
			}
			reader := bufio.NewScanner(response.Body)
			reader.Buffer(make([]byte, 4096), backend.MaxHistoryBytes)
			page, id := ssePage(t, reader)
			if len(page.Messages) != 0 || id != "" || page.Cursor != "" {
				t.Fatal("empty archive invented position", page, id)
			}
			lastID := ""
			for i := 0; i < 2; i++ {
				requestID := fmt.Sprint("stream-live-", i)
				if err := author.SendWithID("#gateway-fixture", "whole live\nbody "+requestID, requestID); err != nil {
					t.Fatal(err)
				}
				receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == requestID })
				page, id = ssePage(t, reader)
				if len(page.Messages) != 1 || page.Messages[0].ID != receipt.ID || page.Messages[0].Message != receipt.Message || id == "" || id != page.Cursor || id == receipt.ID {
					t.Fatal("stream lost live record or exposed raw ID", page, id)
				}
				lastID = id
				select {
				case <-f.watchJoined:
					t.Fatal("stream closed its lease between live events")
				default:
				}
			}
			cancel()
			_ = response.Body.Close()
			select {
			case <-f.watchJoined:
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled SSE did not join upstream")
			}
			// The same operation's opaque checkpoint resumes via Last-Event-ID. This
			// fixture proxy owns one connection, so resume validation below is local.
			for _, test := range []struct {
				id, token string
				status    int
			}{{lastID + "x", f.token, 400}, {"raw-upstream-id", f.token, 400}, {lastID, f.other, 403}} {
				request, _ := http.NewRequest(http.MethodGet, f.server.URL+"/v1/events?"+query.Encode(), nil)
				request.Header.Set("Authorization", "Bearer "+test.token)
				request.Header.Set("Last-Event-ID", test.id)
				response, err := f.server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if response.StatusCode != test.status {
					t.Fatal("resume denial failed", response.StatusCode, test.status)
				}
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			status, _, _ := httpCall(t, f, http.MethodGet, "/v1/events?"+query.Encode(), f.token, nil)
			if status != 403 {
				t.Fatal("SSE bypassed revocation", status)
			}
		})
	}
}
