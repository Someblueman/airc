package core

import (
	"encoding/json"
	"testing"
)

func TestChannelsReadExactGlobalRequest(t *testing.T) {
	valid := Request{Version: 1, RequestID: "channels", Operation: ChannelsRead}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Request){
		"room":    func(r *Request) { r.Target = &Target{Kind: "room", Name: "#room"} },
		"direct":  func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
		"query":   func(r *Request) { r.Query = "#room" },
		"cursor":  func(r *Request) { r.Cursor = "*" },
		"limit":   func(r *Request) { r.Limit = 1 },
		"parent":  func(r *Request) { r.ParentID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"bytes":   func(r *Request) { r.MaxBytes = 1024 },
		"start":   func(r *Request) { r.Start = "recent" },
		"sender":  func(r *Request) { r.From = "alice" },
		"message": func(r *Request) { r.Message = "hello" },
		"policy":  func(r *Request) { p := Retry; r.DeliveryPolicy = &p },
		"expiry":  func(r *Request) { value := int64(1); r.ExpiresAt = &value },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			change(&bad)
			body, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(body); err == nil {
				t.Fatal("accepted unrelated global discovery field")
			}
		})
	}
	for _, body := range []string{
		`{"version":1,"request_id":"channels","operation":"channels.read","target":null}`,
		`{"version":1,"request_id":"channels","operation":"channels.read","operation":"channels.read"}`,
		`{"version":1,"request_id":"channels","Operation":"channels.read"}`,
	} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Fatal("accepted noncanonical request", body)
		}
	}
}
