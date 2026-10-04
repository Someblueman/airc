//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPHTTPEventsWatchProgressCarriesNativeSignalAndCancels(t *testing.T) {
	progress := make(chan *mcp.ProgressNotificationClientRequest, 16)
	f := newMCPHTTPFixture(t, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, notification *mcp.ProgressNotificationClientRequest) {
		select {
		case progress <- notification:
		default:
		}
	}})
	author := dial(t, f.config.Bindings[0].Address, "mcp-live-author", "", false, true)
	defer author.Close()
	request := core.Request{Version: 1, RequestID: "mcp-events-watch", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	params := &mcp.CallToolParams{Name: string(core.EventsWatch), Arguments: json.RawMessage(body)}
	params.SetProgressToken("mcp-events-watch-progress")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, callErr := f.client.CallTool(ctx, params)
		done <- callErr
	}()
	seen, initial := false, false
	deadline := time.After(6 * time.Second)
	for !seen {
		select {
		case notification := <-progress:
			if notification.Params.ProgressToken != "mcp-events-watch-progress" {
				t.Fatalf("wrong MCP progress token: %#v", notification)
			}
			var page liveWirePage
			if err := json.Unmarshal([]byte(notification.Params.Message), &page); err != nil {
				t.Fatal(err)
			}
			if !initial {
				initial = true
				triggerLiveSignal(t, author, "typing")
			}
			for _, raw := range page.Events {
				event := decodeLiveEvent(t, raw)
				var signal irc.ChatEntry
				if json.Unmarshal(event.Signal, &signal) == nil && signal.Action == "typing" {
					seen = true
				}
			}
		case <-deadline:
			t.Fatal("MCP events.watch emitted no native signal progress")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP events.watch cancellation did not join")
	}
}

func TestMCPHTTPEventsWatchOrdinaryPagePreservesRetainedMessage(t *testing.T) {
	f := newMCPHTTPFixture(t)
	author := dial(t, f.config.Bindings[0].Address, "mcp-events-snapshot", "", false, true)
	defer author.Close()
	if err := author.SendWithID("#gateway-fixture", "MCP events retained 🙂", "mcp-events-retained"); err != nil {
		t.Fatal(err)
	}
	receipt := next(t, author, func(event *irc.SendReceiptEvent) bool { return event.RequestID == "mcp-events-retained" })
	request := core.Request{Version: 1, RequestID: "mcp-events-snapshot", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 8, Start: "retained"}
	call := mcpCallRequest(t, f.client, request)
	var page backend.HistoryPage
	if err := json.Unmarshal(mcpStructured(t, call), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != receipt.ID || page.Messages[0].Message != "MCP events retained 🙂" || len(page.Events) != 0 || page.Cursor == "" {
		t.Fatalf("ordinary events.watch page lost retained record: %+v", page)
	}
}
