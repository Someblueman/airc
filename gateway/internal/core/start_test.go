package core

import (
	"strings"
	"testing"
)

func TestInitialHistorySelector(t *testing.T) {
	for _, op := range []Operation{HistoryRead, Watch, EventsWatch} {
		for _, start := range []string{"recent", "retained", strings.Repeat("0f", 16)} {
			r := Request{Version: 1, RequestID: "read", Operation: op, Target: &Target{Kind: "room", Name: "#room"}, Limit: 1, Start: start}
			if err := r.Normalize(); err != nil {
				t.Fatal(err)
			}
			r.Cursor = "opaque"
			if err := r.Normalize(); err == nil {
				t.Fatal("resumed read accepted initial selector")
			}
		}
	}
	for _, op := range []Operation{Send, Reply, ContextRead, ReceiptGet, Search, Directory, CapabilitiesGet} {
		r := Request{Version: 1, RequestID: "read", Operation: op}
		switch op {
		case Send, Reply:
			r.Target, r.Message = &Target{Kind: "room", Name: "#room"}, "body"
			if op == Reply {
				r.ParentID = strings.Repeat("a", 32)
			}
		case ReceiptGet:
			r.LookupID = "other"
		case ContextRead:
			r.Target, r.ParentID = &Target{Kind: "room", Name: "#room"}, strings.Repeat("a", 32)
		case Search:
			r.Target, r.Query, r.Limit = &Target{Kind: "room", Name: "#room"}, "body", 1
		case Directory:
			r.Limit = 1
		}
		if err := r.Normalize(); err != nil {
			t.Fatal("invalid baseline", op, err)
		}
		r.Start = "retained"
		if err := r.Normalize(); err == nil {
			t.Fatal("initial selector accepted on non-history operation", op)
		}
	}
	for _, start := range []string{"arbitrary", strings.Repeat("0F", 16), strings.Repeat("a", 31), "*"} {
		r := Request{Version: 1, RequestID: "read", Operation: Watch, Target: &Target{Kind: "room", Name: "#room"}, Limit: 1, Start: start}
		if err := r.Normalize(); err == nil {
			t.Fatalf("invalid selector %q accepted", start)
		}
	}
}
