package core

import (
	"strings"
	"testing"
)

func TestEventsWatchSelectorsAndMutationBoundary(t *testing.T) {
	base := Request{Version: 1, RequestID: "live", Operation: EventsWatch, Target: &Target{Kind: "room", Name: "#Case"}, Limit: 10}
	for _, mode := range []string{"", "thread", "replies"} {
		r := base
		r.Start = "retained"
		r.ThreadMode = mode
		if mode != "" {
			r.ParentID = strings.Repeat("a", 32)
		}
		if err := r.Normalize(); err != nil {
			t.Fatal(mode, err)
		}
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Message = "mutation" },
		func(r *Request) { r.Query = "search" },
		func(r *Request) { r.ParentID = strings.Repeat("a", 32) },
		func(r *Request) { r.ThreadMode = "thread" },
		func(r *Request) { r.Start = "recent"; r.Cursor = "opaque" },
		func(r *Request) { r.Limit = 0 },
		func(r *Request) { r.Target = nil },
	} {
		r := base
		change(&r)
		if err := r.Normalize(); err == nil {
			t.Fatal("accepted invalid live read", r)
		}
	}
}
