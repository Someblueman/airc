package core

import (
	"strings"
	"testing"
)

func TestPollResultsRequestBounds(t *testing.T) {
	valid := Request{Version: 1, RequestID: "poll-1", Operation: PollResults, Target: &Target{Kind: "room", Name: "#room"}, PollID: strings.Repeat("a", 32)}
	if err := valid.Normalize(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){
		"missing id":      func(r *Request) { r.PollID = "" },
		"uppercase id":    func(r *Request) { r.PollID = strings.Repeat("A", 32) },
		"short id":        func(r *Request) { r.PollID = "a" },
		"direct target":   func(r *Request) { r.Target = &Target{Kind: "direct", Name: "alice"} },
		"missing target":  func(r *Request) { r.Target = nil },
		"limit":           func(r *Request) { r.Limit = 1 },
		"parent":          func(r *Request) { r.ParentID = strings.Repeat("a", 32) },
		"cursor":          func(r *Request) { r.Cursor = "cursor" },
		"query":           func(r *Request) { r.Query = "question" },
		"sender":          func(r *Request) { r.From = "alice" },
		"max bytes":       func(r *Request) { r.MaxBytes = 1 },
		"start":           func(r *Request) { r.Start = "recent" },
		"thread mode":     func(r *Request) { r.ThreadMode = "thread" },
		"delivery policy": func(r *Request) { policy := Retry; r.DeliveryPolicy = &policy },
		"issued at":       func(r *Request) { at := int64(1); r.IssuedAt = &at },
		"expires at":      func(r *Request) { at := int64(2); r.ExpiresAt = &at },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			target := *valid.Target
			r.Target = &target
			mutate(&r)
			if err := r.Normalize(); err == nil {
				t.Fatal("accepted invalid poll results request")
			}
		})
	}
}

func TestPollIDRejectedOnOtherOperations(t *testing.T) {
	parent := strings.Repeat("a", 32)
	valid := []Request{
		{Version: 1, RequestID: "capabilities", Operation: CapabilitiesGet},
		{Version: 1, RequestID: "send", Operation: Send, Target: &Target{Kind: "room", Name: "#room"}, Message: "body"},
		{Version: 1, RequestID: "reply", Operation: Reply, Target: &Target{Kind: "room", Name: "#room"}, Message: "body", ParentID: parent},
		{Version: 1, RequestID: "reaction", Operation: ReactionAdd, Target: &Target{Kind: "room", Name: "#room"}, ParentID: parent, Reaction: "agree"},
		{Version: 1, RequestID: "context", Operation: ContextRead, Target: &Target{Kind: "room", Name: "#room"}, ParentID: parent},
		{Version: 1, RequestID: "history", Operation: HistoryRead, Target: &Target{Kind: "room", Name: "#room"}, Limit: 1},
		{Version: 1, RequestID: "watch", Operation: Watch, Target: &Target{Kind: "room", Name: "#room"}, Limit: 1},
		{Version: 1, RequestID: "search", Operation: Search, Target: &Target{Kind: "room", Name: "#room"}, Limit: 1, Query: "body"},
		{Version: 1, RequestID: "directory", Operation: Directory, Limit: 1},
		{Version: 1, RequestID: "receipt", Operation: ReceiptGet, LookupID: "lookup"},
		{Version: 1, RequestID: "pins", Operation: PinsRead, Target: &Target{Kind: "room", Name: "#room"}},
		{Version: 1, RequestID: "room", Operation: RoomRead, Target: &Target{Kind: "room", Name: "#room"}},
		{Version: 1, RequestID: "waiting", Operation: WaitingRead, Target: &Target{Kind: "room", Name: "#room"}, ParentID: parent},
	}
	for _, request := range valid {
		if err := request.Normalize(); err != nil {
			t.Fatalf("invalid %s control: %v", request.Operation, err)
		}
		request.PollID = parent
		if err := request.Normalize(); err == nil {
			t.Errorf("%s accepted poll ID", request.Operation)
		}
	}
}
