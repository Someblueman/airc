package core

import (
	"strings"
	"testing"
)

func TestRoomReadRequestBounds(t *testing.T) {
	r := Request{Version: 1, RequestID: "room-1", Operation: RoomRead, Target: &Target{Kind: "room", Name: "#room"}}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"direct": func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
		"limit":  func(r *Request) { r.Limit = 1 },
		"query":  func(r *Request) { r.Query = "x" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := r
			target := *r.Target
			copy.Target = &target
			mutate(&copy)
			if err := copy.Normalize(); err == nil {
				t.Fatal("accepted invalid room request")
			}
		})
	}
}

func TestWaitingReadRequestBounds(t *testing.T) {
	r := Request{Version: 1, RequestID: "waiting-1", Operation: WaitingRead, Target: &Target{Kind: "room", Name: "#room"}, ParentID: strings.Repeat("a", 32)}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"uppercase parent": func(r *Request) { r.ParentID = strings.Repeat("A", 32) },
		"missing parent":   func(r *Request) { r.ParentID = "" },
		"cursor":           func(r *Request) { r.Cursor = "cursor" },
		"direct":           func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
	} {
		t.Run(name, func(t *testing.T) {
			copy := r
			target := *r.Target
			copy.Target = &target
			mutate(&copy)
			if err := copy.Normalize(); err == nil {
				t.Fatal("accepted invalid waiting request")
			}
		})
	}
}
