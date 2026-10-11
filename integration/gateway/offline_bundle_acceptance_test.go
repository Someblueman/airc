//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestOfflineBundlePartialCapacityReportAndMixedReplay(t *testing.T) {
	f := newOfflineBundleFixture(t)
	f.config.Limits.Pending, f.config.Limits.PendingPerAuthor = 1, 1
	data, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	tokens := make([]string, 2)
	for i := range tokens {
		tokens[i] = f.credentials.token(t, core.Request{Version: 1, RequestID: fmt.Sprintf("offline-capacity-%d", i), Operation: core.Send, Target: room, Message: fmt.Sprintf("offline capacity body %d 🙂", i)}, false)
	}
	partial := offlineBundleCall(t, f, tokens, false)
	if len(partial.Entries) != 1 || !partial.Entries[0].Imported || partial.Entries[0].Result.State != core.Accepted {
		t.Fatal("partial admission was lost or hidden", partial)
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 1 || native[0].Message != "offline capacity body 0 🙂" {
		t.Fatal("wrong partial native acceptance", native)
	}
	mixed := offlineBundleCall(t, f, tokens, false)
	if len(mixed.Entries) != 2 || !mixed.Entries[0].ReplayRejected || mixed.Entries[0].Imported || !mixed.Entries[1].Imported || mixed.Entries[1].ReplayRejected {
		t.Fatal("mixed replay report lost entry distinctions", mixed)
	}
	for _, entry := range mixed.Entries {
		if entry.Result.State != core.Accepted || entry.Result.SendAttempts != 1 {
			t.Fatal("wrong mixed native receipt", entry)
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Upstream.Address, room.Name); len(native) != 2 {
		t.Fatal("mixed replay lost or duplicated native work", native)
	}
}
