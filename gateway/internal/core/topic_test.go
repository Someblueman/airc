package core

import (
	"strings"
	"testing"
	"time"
)

func TestTopicSetNormalizeAndAcknowledgement(t *testing.T) {
	topic := "  hello  "
	r := Request{Version: 1, RequestID: "topic", Operation: TopicSet, Target: &Target{Kind: "room", Name: "#room"}, Topic: &topic}
	if err := r.Normalize(); err != nil || *r.Topic != "hello" {
		t.Fatalf("topic normalize: %v %q", err, *r.Topic)
	}
	setAt := time.Unix(100, 0).UTC()
	ack := &TopicAcknowledgement{Version: 1, Operation: TopicSet, Topic: TopicResult{Target: *r.Target, Topic: "hello", SetBy: "Agent", SetAt: setAt}}
	if _, err := TopicAckBytes(ack, r, "Agent"); err != nil {
		t.Fatal(err)
	}
	ack.Topic.SetAt = time.Time{}
	if _, err := TopicAckBytes(ack, r, "Agent"); err == nil {
		t.Fatal("accepted missing topic timestamp")
	}
	untrimmed := " hello "
	badRequest := r
	badRequest.Topic = &untrimmed
	ack.Topic.SetAt = setAt
	if _, err := TopicAckBytes(ack, badRequest, "Agent"); err == nil {
		t.Fatal("accepted unnormalized topic intent")
	}
}

func TestTopicSetClearAndBounds(t *testing.T) {
	clear := ""
	r := Request{Version: 1, RequestID: "clear", Operation: TopicSet, Target: &Target{Kind: "room", Name: "#room"}, Topic: &clear}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	ack := &TopicAcknowledgement{Version: 1, Operation: TopicSet, Topic: TopicResult{Target: *r.Target}}
	if _, err := TopicAckBytes(ack, r, "Agent"); err != nil {
		t.Fatal(err)
	}
	tooLong := strings.Repeat("x", 401)
	invalid := Request{Version: 1, RequestID: "long", Operation: TopicSet, Target: &Target{Kind: "room", Name: "#room"}, Topic: &tooLong}
	if err := invalid.Normalize(); err == nil {
		t.Fatal("accepted oversized topic")
	}
	for name, value := range map[string]string{"leading newline": "\nhello", "401 spaces": strings.Repeat(" ", 401)} {
		t.Run(name, func(t *testing.T) {
			bad := Request{Version: 1, RequestID: "bad", Operation: TopicSet, Target: &Target{Kind: "room", Name: "#room"}, Topic: &value}
			if err := bad.Normalize(); err == nil {
				t.Fatal("accepted topic invalid before trimming")
			}
		})
	}
	for _, op := range []Operation{Send, Reply, ReactionAdd, TopicRead, RoomRead} {
		bad := Request{Version: 1, RequestID: "foreign", Operation: op, Topic: &clear}
		if op == Send {
			bad.Target = &Target{Kind: "room", Name: "#room"}
			bad.Message = "message"
		}
		if op == Reply || op == ReactionAdd {
			bad.Target = &Target{Kind: "room", Name: "#room"}
			bad.Message, bad.ParentID = "message", strings.Repeat("a", 32)
		}
		if op == TopicRead || op == RoomRead {
			bad.Target = &Target{Kind: "room", Name: "#room"}
		}
		if err := bad.Normalize(); err == nil {
			t.Fatalf("accepted topic field on %s", op)
		}
	}
	for name, mutate := range map[string]func(*Request){
		"message":  func(r *Request) { r.Message = "body" },
		"reaction": func(r *Request) { r.Reaction = "agree" },
		"parent":   func(r *Request) { r.ParentID = strings.Repeat("a", 32) },
	} {
		t.Run(name, func(t *testing.T) {
			value := "topic"
			bad := Request{Version: 1, RequestID: "foreign", Operation: TopicSet, Target: &Target{Kind: "room", Name: "#room"}, Topic: &value}
			mutate(&bad)
			if err := bad.Normalize(); err == nil {
				t.Fatal("accepted unrelated topic mutation field")
			}
		})
	}
}

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
