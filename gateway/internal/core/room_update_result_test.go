package core

import (
	"encoding/json"
	"testing"
)

func TestResultJSONIncludesRoomAcknowledgement(t *testing.T) {
	ack := &RoomAcknowledgement{Version: 1, Operation: RoomUpdate, Room: RoomResult{Target: Target{Kind: "room", Name: "#room"}, SlowSeconds: 30, HistoryLimit: 10}}
	data, err := json.Marshal(Result{Version: 1, RequestID: "room", State: Accepted, Room: ack})
	if err != nil || !json.Valid(data) || len(data) == 0 {
		t.Fatalf("room result JSON: %s %v", data, err)
	}
	var decoded Result
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Room == nil || decoded.Room.Room != ack.Room {
		t.Fatalf("room acknowledgement missing: %s %v", data, err)
	}
}
