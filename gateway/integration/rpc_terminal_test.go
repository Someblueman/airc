//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestRPCQuietStreamReturnsCorrelatedTerminalAndClosesLease(t *testing.T) {
	lease := shortGatewayTimer(t, readLeaseTimer)
	f := newRPCHTTPFixture(t, false)
	seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "rpc-terminal-parent")
	statusClient := nativeStatusClient(t, f.config.Upstream.Address)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "rpc-terminal", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	ctx, cancel := context.WithTimeout(t.Context(), lease+10*time.Second)
	defer cancel()
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+f.address+"/v1/rpc/events", strings.NewReader(string(rpcRequestBody(t, "terminal-id", "airc.request", rpcRequestParams(request)))))
	if err != nil {
		t.Fatal(err)
	}
	setAuthorHeaders(call, f.token)
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("Accept", "text/event-stream")
	response, err := f.http2.Do(call)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	id, initial := rpcSSEPage(t, readRPCSSE(t, reader))
	if id != "terminal-id" || initial.Cursor == "" {
		t.Fatalf("initial id=%s page=%+v", id, initial)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	lastCursor := initial.Cursor
	terminal := readRPCSSE(t, reader)
	for i := 0; i < 12; i++ {
		var envelope struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(terminal.data, &envelope) != nil || envelope.Method != "airc.page" {
			break
		}
		pageID, page := rpcSSEPage(t, terminal)
		if pageID != "terminal-id" || page.Cursor == "" || len(page.Messages) != 0 {
			t.Fatalf("quiet page=%s", terminal.data)
		}
		lastCursor = page.Cursor
		terminal = readRPCSSE(t, reader)
	}
	value := decodeRPC(t, terminal.data)
	assertRPCID(t, value.ID, json.RawMessage(`"terminal-id"`))
	var result struct {
		Reason string `json:"reason"`
		Page   struct {
			Cursor string `json:"cursor"`
		} `json:"page"`
	}
	if terminal.id != "" || terminal.event != "message" || len(value.Error) != 0 || json.Unmarshal(value.Result, &result) != nil || result.Reason != "lease_expired" || result.Page.Cursor != lastCursor {
		t.Fatalf("terminal frame=%+v response=%s", terminal, terminal.data)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
}
