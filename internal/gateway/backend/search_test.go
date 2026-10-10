package backend

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSearchMarkersNeverInventProgressOrHideExpiry(t *testing.T) {
	r := core.Request{Target: &core.Target{Kind: "room", Name: "#room"}, Operation: core.Search, Query: "needle", Cursor: strings.Repeat("a", 32), Limit: 1}
	end := &irc.EndOfHistoryEvent{Target: "#room", Status: "ok"}
	page, err := finishSearch(nil, end, r)
	if err != nil || page.Cursor != r.Cursor || len(page.Messages) != 0 || page.Gap {
		t.Fatal("empty search invented progress", page, err)
	}
	end.Status = "expired"
	page, err = finishSearch(nil, end, r)
	if err != nil || !page.Gap || page.Status != "expired" || page.Cursor != "" {
		t.Fatal("expiry hidden", page, err)
	}
	m := &irc.MessageMetadata{ID: r.Cursor, From: "author", Target: "#room", Message: "needle", Timestamp: time.Now()}
	entry := irc.CheckEntry{Kind: "message", Target: "#room", Message: m, Targets: []int{0}}
	for _, marker := range []*irc.EndOfHistoryEvent{
		{Target: "#room", Status: "expired", Cursor: r.Cursor},
		{Target: "#room", Status: "more", Cursor: strings.Repeat("b", 32)},
		{Target: "#room", Status: "ok"},
		{Target: "#other", Status: "ok", Cursor: r.Cursor},
		{Target: "#room", Status: "unknown", Cursor: r.Cursor},
	} {
		if page, err := finishSearch([]irc.CheckEntry{entry}, marker, r); err == nil || len(page.Messages) != 0 {
			t.Fatal("invalid marker returned page", page, err)
		}
	}
}
