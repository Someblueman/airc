package core

import "testing"

func TestSearchSenderAndQueryBoundaries(t *testing.T) {
	base := Request{Version: 1, RequestID: "search", Operation: Search, Target: &Target{Kind: "room", Name: "#room"}, Query: "CAFÉ", Limit: 1}
	for _, sender := range []string{"Alice", "*", ""} {
		r := base
		r.From = sender
		if err := r.Normalize(); err != nil || sender == "Alice" && r.From != "alice" || sender == "*" && r.From != "" {
			t.Fatal("sender normalization", r, err)
		}
	}
	for _, sender := range []string{"Alice Bob", "#room", "../file", "1nick"} {
		r := base
		r.From = sender
		if err := r.Normalize(); err == nil {
			t.Fatal("invalid sender accepted", sender)
		}
	}
	for _, query := range []string{"", "x\tbad", "x\u0085bad", "x\nbad"} {
		r := base
		r.Query = query
		if err := r.Normalize(); err == nil {
			t.Fatal("invalid query accepted", query)
		}
	}
	r := base
	r.Operation, r.Query, r.From = HistoryRead, "", "alice"
	if err := r.Normalize(); err == nil {
		t.Fatal("history accepted search sender")
	}
}
