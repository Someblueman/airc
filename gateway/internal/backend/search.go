package backend

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// searchPage owns a native SEARCH snapshot on the already authorized lease.
// The SDK's history events expose message/chat metadata, not retained receipts.
func searchPage(ctx context.Context, client *irc.Client, r core.Request, deadline time.Time) (HistoryPage, error) {
	if err := client.Search(nativeSelector(r), r.Query, r.From, r.Cursor, r.Limit); err != nil {
		return HistoryPage{}, ErrReadUnavailable
	}
	var entries []irc.CheckEntry
	bytes := 0
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return HistoryPage{}, ErrReadUnavailable
			}
			switch e := event.(type) {
			case *irc.HistoryEvent:
				if !strings.Contains(strings.ToLower(e.Message), strings.ToLower(r.Query)) || r.From != "" && !strings.EqualFold(e.From, r.From) {
					return HistoryPage{}, ErrReadUnavailable
				}
				if len(entries) >= r.Limit {
					return HistoryPage{}, store.ErrCapacity
				}
				m := &irc.MessageMetadata{ChatMetadata: e.ChatMetadata, ID: e.ID, Seq: e.Seq, From: e.From, Target: e.Target, Message: e.Message, Timestamp: e.Timestamp, ReplyTo: e.ReplyTo, ThreadID: e.ThreadID, Reaction: e.Reaction}
				if !selectorMessageAllowed(m, r) {
					return HistoryPage{}, auth.ErrForbidden
				}
				data, err := json.Marshal(m)
				if err != nil {
					return HistoryPage{}, ErrReadUnavailable
				}
				bytes += len(data)
				if bytes > MaxHistoryBytes {
					return HistoryPage{}, store.ErrCapacity
				}
				entries = append(entries, irc.CheckEntry{Kind: "message", Target: e.Target, Targets: []int{0}, Message: m})
			case *irc.EndOfHistoryEvent:
				if ctx.Err() != nil || !time.Now().Before(deadline) {
					return HistoryPage{}, ErrReadUnavailable
				}
				return finishSearch(entries, e, r)
			case *irc.RawEvent:
				if e.Command == "760" || e.Command == "761" || e.Command != "422" && len(e.Command) == 3 && e.Command[0] >= '4' && e.Command[0] <= '5' {
					return HistoryPage{}, ErrReadUnavailable
				}
			}
		case <-ctx.Done():
			return HistoryPage{}, ErrReadUnavailable
		}
	}
}

func finishSearch(entries []irc.CheckEntry, end *irc.EndOfHistoryEvent, r core.Request) (HistoryPage, error) {
	marker := irc.CheckEntry{Kind: "page", Target: end.Target, Status: end.Status, Cursor: end.Cursor}
	if end.Status == "expired" {
		if len(entries) != 0 || end.Cursor != "" {
			return HistoryPage{}, ErrReadUnavailable
		}
		marker.Status, marker.Gap = "ok", true
	} else if len(entries) == 0 && end.Status == "ok" {
		// SEARCH has no caught-up watermark when there are no matches. Preserve
		// the requested anchored position rather than inventing forward progress.
		if end.Cursor != "" {
			return HistoryPage{}, ErrReadUnavailable
		}
		if r.Cursor != "*" {
			marker.Cursor = r.Cursor
		}
	} else if len(entries) > 0 && end.Cursor != entries[len(entries)-1].Message.ID {
		return HistoryPage{}, ErrReadUnavailable
	}
	page, err := historyPage(append(entries, marker), r)
	if err != nil {
		return HistoryPage{}, err
	}
	if end.Status == "expired" {
		page.Status = "expired"
	}
	return page, nil
}
