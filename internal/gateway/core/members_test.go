package core

import (
	"encoding/json"
	"testing"
)

func TestMembersReadExactRoomRequest(t *testing.T) {
	valid := Request{Version: 1, RequestID: "members", Operation: MembersRead, Target: &Target{Kind: "room", Name: "#room"}}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Request){
		"no target": func(r *Request) { r.Target = nil },
		"direct":    func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
		"cursor":    func(r *Request) { r.Cursor = "*" },
		"limit":     func(r *Request) { r.Limit = 1 },
		"query":     func(r *Request) { r.Query = "alice" },
		"parent":    func(r *Request) { r.ParentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"poll":      func(r *Request) { r.PollID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"sender":    func(r *Request) { r.From = "alice" },
		"start":     func(r *Request) { r.Start = "recent" },
		"max bytes": func(r *Request) { r.MaxBytes = 1024 },
		"message":   func(r *Request) { r.Message = "hello" },
		"policy":    func(r *Request) { p := Retry; r.DeliveryPolicy = &p },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			change(&bad)
			body, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(body); err == nil {
				t.Fatal("accepted unrelated or invalid argument")
			}
		})
	}
	for _, body := range []string{
		`{"version":1,"request_id":"members","operation":"members.read","target":null}`,
		`{"version":1,"request_id":"members","operation":"members.read","target":{"kind":"room","name":"#room"},"target":{"kind":"room","name":"#other"}}`,
		`{"version":1,"request_id":"members","operation":"members.read","Target":{"kind":"room","name":"#room"}}`,
	} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Fatal("accepted noncanonical members request", body)
		}
	}
}
