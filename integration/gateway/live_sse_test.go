package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func liveDetachedStream(t *testing.T, f *reactionTransportFixture, request core.Request, encrypted bool) (*http.Response, *bufio.Scanner) {
	t.Helper()
	profile := "signed"
	if encrypted {
		profile = "encrypted"
	}
	token := f.cred.token(t, request, encrypted)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	requestHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, f.httpURL+"/v1/envelopes/"+profile+"/events", strings.NewReader(token))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	requestHTTP.Header.Set("Content-Type", "application/jose")
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
