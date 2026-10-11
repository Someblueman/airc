//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestHTTP3RPCEventsWatchRetainedLiveAndCancel(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "http3-events-parent")
	author := dial(t, f.config.Upstream.Address, "http3-events-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := core.Request{Version: 1, RequestID: "http3-events", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/rpc/events", strings.NewReader(string(rpcRequestBody(t, request.RequestID, "airc.request", rpcRequestParams(request)))))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(httpRequest, f.token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	response, err := f.client.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertHTTP3Security(t, response)
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		t.Fatalf("HTTP/3 SSE status/protocol/content=%d/%d/%q body=%s", response.StatusCode, response.ProtoMajor, response.Header.Get("Content-Type"), data)
	}
	reader := bufio.NewReader(response.Body)
	_, initial := rpcSSEPage(t, readRPCSSE(t, reader))
	if initial.Cursor == "" || len(initial.Messages) == 0 || initial.Messages[0] == nil || initial.Messages[0].ID != parentID {
		t.Fatalf("HTTP/3 retained page=%+v", initial)
	}
	triggerLiveSignal(t, author, "typing")
	_, live := rpcSSEPage(t, readRPCSSE(t, reader))
	if live.Cursor != initial.Cursor || len(live.Events) == 0 {
		t.Fatalf("HTTP/3 live page cursor/events: initial=%+v live=%+v", initial, live)
	}
	cancel()
}

func TestHTTP3ClassicLiveEvents(t *testing.T) {
	f := newHTTP3GatewayFixture(t)
	seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "http3-classic-parent")
	author := dial(t, f.config.Upstream.Address, "http3-classic-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	query := url.Values{"kind": {"room"}, "target": {"#gateway-fixture"}, "limit": {"8"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+f.address+"/v1/live/events?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(request, f.token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertHTTP3Security(t, response)
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("HTTP/3 classic live status/protocol/content=%d/%d/%q", response.StatusCode, response.ProtoMajor, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	initialEvent := readRPCSSE(t, reader)
	if initialEvent.event != "page" {
		t.Fatalf("HTTP/3 classic initial event=%+v", initialEvent)
	}
	triggerLiveSignal(t, author, "typing")
	event := readRPCSSE(t, reader)
	if event.event != "page" {
		t.Fatalf("HTTP/3 classic live event=%+v", event)
	}
	var page backend.HistoryPage
	if err := json.Unmarshal(event.data, &page); err != nil || len(page.Events) == 0 {
		t.Fatalf("HTTP/3 classic live page=%s err=%v", event.data, err)
	}
	cancel()
}
