package backend

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// Watches count against both the lookup/send reserve and separate watch limits.
func (b *Backend) acquireWatch(principal string) bool {
	if !b.acquire(principal, store.LookupAttempt) {
		return false
	}
	b.mu.Lock()
	if b.watchTotal >= b.watchLimit || b.watchActive[principal] >= b.principalWatchLimit {
		b.mu.Unlock()
		b.release(principal, store.LookupAttempt)
		return false
	}
	b.watchTotal++
	b.watchActive[principal]++
	b.mu.Unlock()
	return true
}

func (b *Backend) releaseWatch(principal string) {
	b.mu.Lock()
	b.watchTotal--
	b.watchActive[principal]--
	b.mu.Unlock()
	b.release(principal, store.LookupAttempt)
}

// Subscribe before CHECK. The exclusive event owner buffers intervening live
// records, then deduplicates them against the snapshot. No cursor is persisted.
func watchPage(ctx context.Context, client *irc.Client, r core.Request, deadline time.Time) (HistoryPage, error) {
	buffer, err := observeWatch(ctx, client, r)
	if err != nil {
		return HistoryPage{}, err
	}
	return watchedSnapshot(ctx, client, r, deadline, buffer)
}

func observeWatch(ctx context.Context, client *irc.Client, r core.Request) (*watchBuffer, error) {
	selector := nativeSelector(r)
	if err := client.Observe(selector); err != nil {
		return nil, ErrReadUnavailable
	}
	buffer := &watchBuffer{request: r}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, ErrReadUnavailable
			}
			if err := buffer.collect(event); err != nil {
				return nil, err
			}
			if raw, ok := event.(*irc.RawEvent); ok {
				if raw.Command == "765" {
					if len(raw.Params) != 2 || raw.Params[1] != selector {
						return nil, ErrReadUnavailable
					}
					return buffer, nil
				}
				if rejectedRead(raw) {
					return nil, ErrReadUnavailable
				}
			}
		case <-ctx.Done():
			return nil, ErrReadUnavailable
		}
	}
}

func watchedSnapshot(ctx context.Context, client *irc.Client, r core.Request, deadline time.Time, buffer *watchBuffer) (HistoryPage, error) {
	entries, err := boundedHistoryCheck(ctx, client, r, buffer.collect)
	if err != nil {
		return HistoryPage{}, err
	}
	page, err := historyPage(entries, r)
	if err != nil {
		return HistoryPage{}, err
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return HistoryPage{}, ErrReadUnavailable
	}
	page, err = mergeWatch(page, buffer.messages, r)
	if err != nil {
		return HistoryPage{}, err
	}
	if page.Status == "more" || page.Gap {
		// Resume the contiguous snapshot prefix; later live records remain
		// recoverable from retained history on the next request.
		return page, nil
	}
	if len(page.Messages) > 0 {
		return page, nil
	}
	// An empty poll succeeds before the enclosing HTTP/deadline bound. Noise
	// never resets this timer. Renewing the request rechecks authorization.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return HistoryPage{}, ErrReadUnavailable
			}
			if raw, ok := event.(*irc.RawEvent); ok && rejectedRead(raw) {
				return HistoryPage{}, ErrReadUnavailable
			}
			if err := buffer.collect(event); err != nil {
				return HistoryPage{}, err
			}
			if len(buffer.messages) > 0 {
				return mergeWatch(page, buffer.messages, r)
			}
		case <-timer.C:
			if ctx.Err() != nil || !time.Now().Before(deadline) {
				return HistoryPage{}, ErrReadUnavailable
			}
			return page, nil
		case <-ctx.Done():
			return HistoryPage{}, ErrReadUnavailable
		}
	}
}

type watchBuffer struct {
	request  core.Request
	messages []*irc.MessageMetadata
	bytes    int
}

func (b *watchBuffer) collect(event irc.Event) error {
	e, ok := event.(*irc.MessageEvent)
	if !ok || e.Type != "message" {
		return nil
	}
	m := &irc.MessageMetadata{ChatMetadata: e.ChatMetadata, ID: e.ID, From: e.From, Target: e.Target, Message: e.Message, Timestamp: e.Timestamp, ReplyTo: e.ReplyTo, ThreadID: e.ThreadID, Reaction: e.Reaction}
	// Validate even duplicates; unrelated room data fails closed.
	if _, err := historyPage([]irc.CheckEntry{watchEntry(m), {Kind: "page", Target: nativeSelector(b.request), Status: "ok", Cursor: m.ID}}, b.request); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return ErrReadUnavailable
	}
	b.bytes += len(data)
	if b.bytes > MaxHistoryBytes || len(b.messages) >= b.request.Limit {
		return store.ErrCapacity
	}
	b.messages = append(b.messages, m)
	return nil
}

func rejectedRead(raw *irc.RawEvent) bool {
	return raw.Command != "422" && len(raw.Command) == 3 && raw.Command[0] >= '4' && raw.Command[0] <= '5'
}

func watchEntry(m *irc.MessageMetadata) irc.CheckEntry {
	return irc.CheckEntry{Kind: "message", Target: m.Target, Targets: []int{0}, Message: m}
}

func mergeWatch(page HistoryPage, live []*irc.MessageMetadata, r core.Request) (HistoryPage, error) {
	seen := make(map[string]*irc.MessageMetadata, len(page.Messages))
	var entries []irc.CheckEntry
	for _, m := range page.Messages {
		seen[m.ID] = m
		entries = append(entries, watchEntry(m))
	}
	for _, m := range live {
		if _, err := historyPage([]irc.CheckEntry{watchEntry(m), {Kind: "page", Target: nativeSelector(r), Status: "ok", Cursor: m.ID}}, r); err != nil {
			return HistoryPage{}, err
		}
		if prior := seen[m.ID]; prior != nil {
			if !sameWatchMessage(prior, m) {
				return HistoryPage{}, ErrReadUnavailable
			}
			continue
		}
		seen[m.ID] = m
		if page.Status == "more" || page.Gap {
			// Correlate all overlap, but preserve the unread snapshot prefix.
			continue
		}
		if len(entries) >= r.Limit {
			return HistoryPage{}, store.ErrCapacity
		}
		entries = append(entries, watchEntry(m))
		page.Cursor = m.ID
	}
	return historyPage(append(entries, irc.CheckEntry{Kind: "page", Target: nativeSelector(r), Status: page.Status, Cursor: page.Cursor, Gap: page.Gap}), r)
}

func sameWatchMessage(prior, m *irc.MessageMetadata) bool {
	return !(prior.From != m.From || prior.Target != m.Target || prior.Message != m.Message || !prior.Timestamp.Equal(m.Timestamp) || prior.ReplyTo != m.ReplyTo || prior.ThreadID != m.ThreadID || prior.Reaction != m.Reaction || prior.AccountID != m.AccountID || prior.RequestID != m.RequestID || prior.Kind != m.Kind)
}
