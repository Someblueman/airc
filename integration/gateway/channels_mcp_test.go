//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestMCPHTTPChannelsReadReturnsNativeCatalogue(t *testing.T) {
	f := newMCPHTTPFixture(t)
	live := seedChannels(t, f.config.Bindings[0].Address)
	beforeHistory := channelHistorySnapshot(t, f.config.Bindings[0].Address)
	want := nativeChannels(t, f.config.Bindings[0].Address)
	call := mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-channels-read", Operation: core.ChannelsRead})
	var got core.ChannelsResult
	if err := json.Unmarshal(mcpStructured(t, call), &got); err != nil {
		t.Fatal(err)
	}
	if got.Channels == nil || len(got.Channels) != len(want) {
		t.Fatalf("MCP channels=%+v want=%+v", got.Channels, want)
	}
	for i := range want {
		if got.Channels[i] != want[i] {
			t.Fatalf("MCP channels[%d]=%+v want=%+v", i, got.Channels[i], want[i])
		}
	}
	if err := live.Part("#channels-live", "done"); err != nil {
		t.Fatal(err)
	}
	next(t, live, func(e *irc.PartEvent) bool { return e.Agent == "channels-live" })
	after := nativeChannels(t, f.config.Bindings[0].Address)
	call = mcpCallRequest(t, f.client, core.Request{Version: 1, RequestID: "mcp-channels-read", Operation: core.ChannelsRead})
	var fresh core.ChannelsResult
	if err := json.Unmarshal(mcpStructured(t, call), &fresh); err != nil || !channelsEqual(fresh.Channels, after) || channelsEqual(want, after) {
		t.Fatalf("fresh MCP snapshot=%+v native=%+v before=%+v err=%v", fresh, after, want, err)
	}
	if !reflect.DeepEqual(beforeHistory, channelHistorySnapshot(t, f.config.Bindings[0].Address)) {
		t.Fatal("MCP discovery changed native history")
	}
}
