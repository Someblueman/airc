package gateway_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPEventsRetainedPrefixResumeAndLeaseExpiry(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, f.socket, "fixture-author", account, false, true)
	ids := []string{}
	for i := 0; i < 3; i++ {
		request := fmt.Sprint("sse-retained-", i)
		if err := author.SendWithID("#gateway-fixture", request, request); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == request })
		ids = append(ids, receipt.ID)
	}
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}, "start": {"retained"}}
	cursor := ""
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/v1/events?"+query.Encode(), nil)
		request.Header.Set("Authorization", "Bearer "+f.token)
		if cursor != "" {
			request.Header.Set("Last-Event-ID", cursor)
		}
		response, err := f.server.Client().Do(request)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		reader := bufio.NewScanner(response.Body)
		reader.Buffer(make([]byte, 4096), backend.MaxHistoryBytes)
		page, id := ssePage(t, reader)
		if response.StatusCode != 200 || len(page.Messages) != 1 || page.Messages[0].ID != ids[i] || id == "" || page.Cursor != id {
			cancel()
			_ = response.Body.Close()
			t.Fatal("retained resume skipped prefix", page, id, response.Status)
		}
		if i < 2 {
			if page.Status != "more" {
				t.Fatal("unread retained prefix not marked", page)
			}
			end := false
			for reader.Scan() {
				if reader.Text() == `data: {"reason":"resume"}` {
					end = true
				}
			}
			if !end || reader.Err() != nil {
				t.Fatal("catch-up lease did not end for resume", reader.Err())
			}
		} else {
			if page.Status != "ok" {
				t.Fatal("caught-up checkpoint not established", page)
			}
			// A quiet lease must outlive the server's normal five-second write bound,
			// then end by its authorization deadline rather than SDK idle timeout.
			expired := false
			began := time.Now()
			for reader.Scan() {
				if strings.Contains(reader.Text(), `"lease_expired"`) {
					expired = true
				}
			}
			if reader.Err() != nil || !expired || time.Since(began) < 10*time.Second || time.Since(began) > 18*time.Second {
				t.Fatal("stream lease/keepalive bound failed", expired, time.Since(began), reader.Err())
			}
		}
		cursor = id
		query.Del("start")
		cancel()
		_ = response.Body.Close()
	}
}
