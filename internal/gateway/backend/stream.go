package backend

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

var ErrStreamRenew = errors.New("stream lease expired")

// streamWatch owns one subscription for the whole authorized lease. The sink
// must bound its writes and return errors synchronously; there is no send queue.
func streamWatch(ctx context.Context, client *irc.Client, r core.Request, deadline time.Time, emit func(HistoryPage) error) (HistoryPage, error) {
	setup, cancel := context.WithDeadline(ctx, deadline.Add(-store.ReadLeaseDuration+4*time.Second))
	closed := make(chan struct{})
	stop := context.AfterFunc(setup, func() { _ = client.Close(); close(closed) })
	setupDone := false
	defer func() {
		if !setupDone && !stop() {
			<-closed
		}
		cancel()
	}()
	var buffer *watchBuffer
	var err error
	if r.ThreadMode != "" {
		r, err = prepareSelector(setup, client, r)
		if err != nil {
			return HistoryPage{}, err
		}
	}
	buffer, err = observeWatch(setup, client, r)
	if err != nil {
		return HistoryPage{}, err
	}
	entries, err := boundedHistoryCheck(setup, client, r, buffer.collect)
	if err != nil {
		return HistoryPage{}, err
	}
	page, err := historyPage(entries, r)
	if err != nil {
		return HistoryPage{}, err
	}
	page, err = mergeWatch(page, buffer.messages, r)
	if err == nil {
		page, err = mergeLiveEvents(setup, client, page, buffer)
	}
	if err != nil {
		return HistoryPage{}, err
	}
	setupDone = true
	if !stop() {
		<-closed
		return HistoryPage{}, ErrReadUnavailable
	}
	cancel()
	if ctx.Err() != nil {
		return HistoryPage{}, ErrReadUnavailable
	}
	if err := emit(page); err != nil {
		return page, err
	}
	if page.Status == "more" || page.Gap {
		return page, nil
	}
	seen := make(map[string]*irc.MessageMetadata, len(page.Messages)+128)
	for _, m := range page.Messages {
		seen[m.ID] = m
	}
	delivered := 0
	if r.Operation == core.EventsWatch {
		delivered = len(buffer.messages) + len(buffer.events)
	}
	buffer = freshWatchBuffer(r, client, buffer)
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return page, streamEnd(deadline)
			}
			if raw, ok := event.(*irc.RawEvent); ok && rejectedRead(raw) {
				return page, ErrReadUnavailable
			}
			if err := buffer.collect(event); err != nil {
				return page, err
			}
			if len(buffer.messages) == 0 && len(buffer.events) == 0 {
				continue
			}
			base := HistoryPage{Messages: []*irc.MessageMetadata{}, Cursor: page.Cursor, Status: "ok"}
			next, err := mergeLiveEvents(ctx, client, base, buffer)
			if r.Operation != core.EventsWatch {
				next, err = mergeWatch(base, buffer.messages, r)
			}
			if err != nil {
				return page, err
			}
			messages := make([]*irc.MessageMetadata, 0, len(next.Messages))
			for _, m := range next.Messages {
				if prior := seen[m.ID]; prior != nil {
					if !sameWatchMessage(prior, m) {
						return page, ErrReadUnavailable
					}
					continue
				}
				messages = append(messages, m)
			}
			events := next.Events
			next, err = mergeWatch(base, messages, r)
			if err != nil {
				return page, err
			}
			next.Events = events
			if len(messages)+len(events) == 0 {
				buffer = freshWatchBuffer(r, client, buffer)
				continue
			}
			if delivered+len(messages)+len(events) > 128 {
				return page, store.ErrCapacity
			}
			if ctx.Err() != nil {
				return page, streamEnd(deadline)
			}
			if err := emit(next); err != nil {
				return page, err
			}
			for _, m := range messages {
				seen[m.ID] = m
			}
			delivered += len(messages) + len(events)
			page = next
			buffer = freshWatchBuffer(r, client, buffer)
		case <-timer.C:
			if ctx.Err() != nil {
				return page, streamEnd(deadline)
			}
			if err := emit(HistoryPage{Messages: []*irc.MessageMetadata{}, Cursor: page.Cursor, Status: "ok"}); err != nil {
				return page, err
			}
		case <-ctx.Done():
			return page, streamEnd(deadline)
		}
	}
}

func streamEnd(deadline time.Time) error {
	if !time.Now().Before(deadline) {
		return ErrStreamRenew
	}
	return ErrReadUnavailable
}
