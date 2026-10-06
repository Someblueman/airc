//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestOfflineBundleNativeAcceptanceAndDurableReplayRejection(t *testing.T) {
	f := newOfflineBundleFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	tokens := make([]string, 2)
	bodies := make([]string, 2)
	for i := range tokens {
		bodies[i] = fmt.Sprintf("offline %d\nwhole Unicode 🙂 $(touch must-not-execute)", i)
		tokens[i] = f.credentials.token(t, core.Request{Version: 1, RequestID: fmt.Sprintf("offline-send-%d", i), Operation: core.Send, Target: room, Message: bodies[i]}, false)
	}
	report := offlineBundleCall(t, f, tokens, true)
	if len(report.Entries) != 2 {
		t.Fatal("missing native import entries", report)
	}
	for i, entry := range report.Entries {
		if !entry.Imported || entry.ReplayRejected || entry.Principal != f.config.Principals[0].ID || entry.Binding != f.config.Principals[0].Binding || entry.Result.State != core.Accepted || !entry.Result.Durable || entry.Result.SendAttempts != 1 {
			t.Fatal("not accepted", entry)
		}
		event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == entry.Result.MessageID })
		if event.From != "fixture-author" || event.Target != room.Name || event.Message != bodies[i] {
			t.Fatal("native observer mismatch", event)
		}
	}
	replay := offlineBundleCall(t, f, tokens, false)
	if len(replay.Entries) != 2 {
		t.Fatal("missing replay report", replay)
	}
	for i, entry := range replay.Entries {
		if entry.Imported || !entry.ReplayRejected || entry.Result.State != core.Accepted || entry.Result.MessageID != report.Entries[i].Result.MessageID || entry.Result.SendAttempts != 1 {
			t.Fatal("replay admitted or changed result", entry)
		}
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 2 {
		t.Fatal("replay duplicated native messages", native)
	}
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal("CLI did not release journal ownership", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineBundlePartialCapacityReportAndMixedReplay(t *testing.T) {
	f := newOfflineBundleFixture(t)
	f.config.Limits.Pending, f.config.Limits.PendingPerPrincipal = 1, 1
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
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 1 || native[0].Message != "offline capacity body 0 🙂" {
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
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name); len(native) != 2 {
		t.Fatal("mixed replay lost or duplicated native work", native)
	}
}
