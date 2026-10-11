package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// liveDetachedStream opens the events.watch SSE stream for request as the
// fixture's registered author: GET /v1/live/events with the request's
// selector as query parameters and the author in headers.
func liveDetachedStream(t *testing.T, f *reactionTransportFixture, request core.Request, _ bool) (*http.Response, *bufio.Scanner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	requestHTTP, err := http.NewRequestWithContext(ctx, http.MethodGet, f.httpURL+"/v1/live/events?"+readQuery(request), nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	setAuthorHeaders(requestHTTP, f.authorSpec)
	response, err := f.http.Do(requestHTTP)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = response.Body.Close() })
	reader := bufio.NewScanner(response.Body)
	reader.Buffer(make([]byte, 4096), 1<<20)
	return response, reader
}

// readQuery is a read request's selector as HTTP GET query parameters.
func readQuery(r core.Request) string {
	query := url.Values{}
	if r.Target != nil {
		query.Set("kind", r.Target.Kind)
		query.Set("target", r.Target.Name)
	}
	if r.Limit != 0 {
		query.Set("limit", strconv.Itoa(r.Limit))
	}
	for key, value := range map[string]string{"start": r.Start, "cursor": r.Cursor, "parent_id": r.ParentID, "thread_mode": r.ThreadMode} {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query.Encode()
}

func nextLiveSSEPage(t *testing.T, reader *bufio.Scanner) liveWirePage {
	t.Helper()
	var data, eventName, eventID string
	for reader.Scan() {
		line := reader.Text()
		if line == "" {
			if eventName == "page" && data != "" {
				var page liveWirePage
				if err := json.Unmarshal([]byte(data), &page); err != nil {
					t.Fatal(err, data)
				}
				if eventID != "" && page.Cursor != eventID {
					t.Fatalf("SSE cursor ID mismatch: id=%q page=%q", eventID, page.Cursor)
				}
				return page
			}
			data, eventName, eventID = "", "", ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "id: ") {
			eventID = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatal("live SSE ended before page", reader.Err())
	return liveWirePage{}
}
