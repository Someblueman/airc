//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
)

type rpcSSE struct {
	event string
	id    string
	data  []byte
}

func readRPCSSE(t *testing.T, reader *bufio.Reader) rpcSSE {
	t.Helper()
	var item rpcSSE
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			if len(item.data) != 0 {
				return item
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			item.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "id:"):
			item.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			item.data = append(item.data, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		}
	}
}

func rpcSSEPage(t *testing.T, event rpcSSE) (string, backend.HistoryPage) {
	t.Helper()
	if event.event != "message" {
		t.Fatalf("unexpected RPC SSE event=%+v", event)
	}
	var notification struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			ID   string              `json:"id"`
			Page backend.HistoryPage `json:"page"`
		} `json:"params"`
	}
	if err := json.Unmarshal(event.data, &notification); err != nil {
		t.Fatal(err)
	}
	if notification.JSONRPC != "2.0" || notification.Method != "airc.page" || notification.Params.ID == "" {
		t.Fatalf("invalid RPC page notification=%s", event.data)
	}
	return notification.Params.ID, notification.Params.Page
}

func TestRPCEventsWatchSSERetainedAndLiveCursor(t *testing.T) {
	f := newRPCHTTPFixture(t, false)
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baselineObservers := nativeObserverCount(t, statusClient)
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "rpc-events-parent")
	author := dial(t, f.config.Upstream.Address, "rpc-events-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	request := core.Request{Version: 1, RequestID: "rpc-events-watch", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := rpcRequestBody(t, "rpc-events-id", "airc.request", rpcRequestParams(request))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/rpc/events", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(httpRequest, f.token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	transport := &http.Transport{TLSClientConfig: f.cert.Client}
	t.Cleanup(transport.CloseIdleConnections)
	response, err := (&http.Client{Transport: transport}).Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("RPC SSE status/content-type=%d/%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), data)
	}
	waitNativeObserverCount(t, statusClient, baselineObservers+1)
	reader := bufio.NewReader(response.Body)
	initialID, initial := rpcSSEPage(t, readRPCSSE(t, reader))
	if initialID != "rpc-events-id" || initial.Cursor == "" || len(initial.Messages) == 0 {
		t.Fatalf("RPC retained page=%+v id=%q", initial, initialID)
	}
	if initial.Messages[0] == nil || initial.Messages[0].ID != parentID {
		t.Fatalf("RPC retained message=%+v want=%s", initial.Messages[0], parentID)
	}
	triggerLiveSignal(t, author, "typing")
	_, live := rpcSSEPage(t, readRPCSSE(t, reader))
	if live.Cursor != initial.Cursor || len(live.Events) == 0 {
		t.Fatalf("RPC signal changed cursor or was lost: initial=%+v live=%+v", initial, live)
	}
	cancel()
	_ = response.Body.Close()
	waitNativeObserverCountAtMost(t, statusClient, baselineObservers)
	select {
	case <-time.After(2 * time.Second):
		// The request context is the cancellation boundary; no terminal frame is required after disconnect.
	case <-ctx.Done():
	}
}
