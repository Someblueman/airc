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

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHTTPEventsRetainedPrefixResumeAndLeaseExpiry(t *testing.T) {
	// The quiet lease must still outlive the server's five-second write bound.
	timer := readLeaseTimer
	timer.short = 8 * time.Second
	lease := shortGatewayTimer(t, timer)
	f := gatewayHTTPFixture(t, true)
	var observed []*irc.MessageEvent
	for i := 0; i < 3; i++ {
		id := fmt.Sprint("sse-retained-", i)
		request := core.Request{Version: 1, RequestID: id, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: id + "\nwhole body 🙂"}
		status, protocol, data := httpCall(t, f, http.MethodPost, "/v1/messages", f.token, requestJSON(t, request))
		var admitted core.Result
		if err := json.Unmarshal(data, &admitted); err != nil || status != 202 || protocol != 2 || !admitted.Durable {
			t.Fatal("SSE companion POST admission failed", status, protocol, string(data), err)
		}
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == request.Message })
		if seen.From != "fixture-author" || seen.Target != request.Target.Name {
			t.Fatal("SSE native observer mismatch", seen)
		}
		observed = append(observed, seen)
	}
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"1"}, "start": {"retained"}}
	cursor := ""
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), lease+5*time.Second)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/v1/events?"+query.Encode(), nil)
		setAuthorHeaders(request, f.token)
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
		if response.StatusCode != 200 || response.ProtoMajor != 2 || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || len(page.Messages) != 1 || page.Messages[0].ID != observed[i].ID || page.Messages[0].Message != observed[i].Message || page.Messages[0].From != observed[i].From || page.Messages[0].Target != observed[i].Target || page.Gap || id == "" || id == observed[i].ID || page.Cursor != id {
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
			if reader.Err() != nil || !expired || time.Since(began) < lease-2*time.Second || time.Since(began) > lease+3*time.Second {
				t.Fatal("stream lease/keepalive bound failed", expired, time.Since(began), reader.Err())
			}
		}
		cursor = id
		query.Del("start")
		cancel()
		_ = response.Body.Close()
	}
}
