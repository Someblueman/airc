package core

import (
	"encoding/json"
	"testing"
)

func TestAgentsReadExactGlobalRequest(t *testing.T) {
	valid := Request{Version: 1, RequestID: "agents", Operation: AgentsRead, Limit: MaxAgents}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Request){
		"target":  func(r *Request) { r.Target = &Target{Kind: "room", Name: "#room"} },
		"query":   func(r *Request) { r.Query = "#room" },
		"cursor":  func(r *Request) { r.Cursor = "opaque" },
		"parent":  func(r *Request) { r.ParentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"thread":  func(r *Request) { r.ThreadMode = "replies" },
		"start":   func(r *Request) { r.Start = "recent" },
		"sender":  func(r *Request) { r.From = "alice" },
		"message": func(r *Request) { r.Message = "hello" },
		"bytes":   func(r *Request) { r.MaxBytes = 1024 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			change(&bad)
			body, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(body); err == nil {
				t.Fatal("accepted unrelated agents.read field")
			}
		})
	}
	for _, limit := range []int{0, MaxAgents + 1} {
		bad := valid
		bad.Limit = limit
		body, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(body); err == nil {
			t.Fatalf("accepted limit %d", limit)
		}
	}
}
