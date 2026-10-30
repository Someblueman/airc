//go:build darwin || linux

package gateway_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestRoomUpdateAppliesRetentionQuota(t *testing.T) {
	t.Parallel()
	f := newReactionFixture(t, true, nil, true)
	promoteRoomOperator(t, f.socket)
	zero, unlimited := int64(0), 0
	request := core.Request{Version: 1, RequestID: "room-quota-initial", Operation: core.RoomUpdate, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Room: &core.RoomPatch{SlowSeconds: &zero, HistoryLimit: &unlimited}}
	headerPost(t, f, request)
	waitHTTPRoomResult(t, f, request.RequestID, core.Accepted)
	before := seedRoomQuotaMessages(t, f.socket, 0, 5)
	if snapshot := topicHistorySnapshot(t, f.socket); len(snapshot) != 5 {
		t.Fatalf("initial retained positive control: %v", snapshot)
	}
	two := 2
	request.RequestID = "room-quota-trim"
	request.Room = &core.RoomPatch{HistoryLimit: &two}
	headerPost(t, f, request)
	waitHTTPRoomResult(t, f, request.RequestID, core.Accepted)
	trimmed := topicHistorySnapshot(t, f.socket)
	if len(trimmed) != 2 || trimmed[before[3]] != "room-quota-3" || trimmed[before[4]] != "room-quota-4" {
		t.Fatalf("quota did not retain only the latest two messages: %v", trimmed)
	}
	request.RequestID = "room-quota-remove"
	request.Room = &core.RoomPatch{HistoryLimit: &unlimited}
	headerPost(t, f, request)
	waitHTTPRoomResult(t, f, request.RequestID, core.Accepted)
	after := seedRoomQuotaMessages(t, f.socket, 5, 3)
	retained := topicHistorySnapshot(t, f.socket)
	if len(retained) != 5 || retained[before[3]] != "room-quota-3" || retained[before[4]] != "room-quota-4" {
		t.Fatalf("zero quota did not preserve and grow retained history: %v", retained)
	}
	for i, id := range after {
		if retained[id] != fmt.Sprintf("room-quota-%d", i+5) {
			t.Fatalf("new message missing after removing quota: %v", retained)
		}
	}
}

func seedRoomQuotaMessages(t *testing.T, socket string, start, count int) []string {
	t.Helper()
	author := dial(t, socket, "fixture-author", fixtureAccount, false, true)
	defer author.Close()
	ids := make([]string, 0, count)
	for i := start; i < start+count; i++ {
		id := fmt.Sprintf("room-quota-%d", i)
		if err := author.SendWithID("#gateway-fixture", id, id); err != nil {
			t.Fatal(err)
		}
		receipt := next(t, author, func(e *irc.SendReceiptEvent) bool { return e.RequestID == id })
		if receipt.ID == "" {
			t.Fatal("quota message has no native ID")
		}
		ids = append(ids, receipt.ID)
	}
	return ids
}
