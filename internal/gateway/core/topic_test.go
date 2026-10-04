package core

import (
	"strings"
	"testing"
)

func TestTopicReadRequestBounds(t *testing.T) {
	valid := Request{Version: 1, RequestID: "topic-1", Operation: TopicRead, Target: &Target{Kind: "room", Name: "#room"}}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"nil target":    func(r *Request) { r.Target = nil },
		"direct target": func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
		"cursor":        func(r *Request) { r.Cursor = "cursor" },
		"query":         func(r *Request) { r.Query = "topic" },
		"limit":         func(r *Request) { r.Limit = 1 },
		"parent":        func(r *Request) { r.ParentID = strings.Repeat("a", 32) },
		"poll":          func(r *Request) { r.PollID = strings.Repeat("a", 32) },
		"sender":        func(r *Request) { r.From = "alice" },
		"lookup":        func(r *Request) { r.LookupID = "request" },
		"max bytes":     func(r *Request) { r.MaxBytes = 4096 },
		"message":       func(r *Request) { r.Message = "topic mutation" },
		"reaction":      func(r *Request) { r.Reaction = "agree" },
		"start":         func(r *Request) { r.Start = "retained" },
		"thread":        func(r *Request) { r.ThreadMode = "thread" },
		"policy":        func(r *Request) { policy := Retry; r.DeliveryPolicy = &policy },
		"issued":        func(r *Request) { at := int64(1); r.IssuedAt = &at },
		"expiry":        func(r *Request) { at := int64(2); r.ExpiresAt = &at },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			target := *valid.Target
			r.Target = &target
			mutate(&r)
			if err := r.Normalize(); err == nil {
				t.Fatal("accepted invalid topic request")
			}
		})
	}
}
