package core

import "testing"

func TestRoomUpdateNormalizeAndAcknowledgement(t *testing.T) {
	slow := int64(30)
	r := Request{Version: 1, RequestID: "room", Operation: RoomUpdate, Target: &Target{Kind: "room", Name: "#room"}, Room: &RoomPatch{SlowSeconds: &slow}}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	ack := &RoomAcknowledgement{Version: 1, Operation: RoomUpdate, Room: RoomResult{Target: *r.Target, SlowSeconds: 30, HistoryLimit: 0}}
	if _, err := RoomAckBytes(ack, r); err != nil {
		t.Fatal(err)
	}
	ack.Room.SlowSeconds = 31
	if _, err := RoomAckBytes(ack, r); err == nil {
		t.Fatal("accepted mismatched room setting")
	}
}

func TestRoomUpdateBoundsAndForeignFields(t *testing.T) {
	negative := int64(-1)
	for _, patch := range []*RoomPatch{nil, {}, {SlowSeconds: &negative}} {
		r := Request{Version: 1, RequestID: "invalid", Operation: RoomUpdate, Target: &Target{Kind: "room", Name: "#room"}, Room: patch}
		if err := r.Normalize(); err == nil {
			t.Fatal("accepted invalid room patch")
		}
	}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Message = "body" },
		func(r *Request) { r.Reaction = "agree" },
		func(r *Request) { r.ParentID = "a" },
		func(r *Request) { topic := "topic"; r.Topic = &topic },
		func(r *Request) { r.Profile = &ProfilePatch{Clear: true} },
		func(r *Request) { r.Presence = &PresenceInput{State: "away", TTLSeconds: 1} },
	} {
		slow := int64(1)
		r := Request{Version: 1, RequestID: "foreign", Operation: RoomUpdate, Target: &Target{Kind: "room", Name: "#room"}, Room: &RoomPatch{SlowSeconds: &slow}}
		mutate(&r)
		if err := r.Normalize(); err == nil {
			t.Fatal("accepted unrelated room update field")
		}
	}
}
