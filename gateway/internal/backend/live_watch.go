package backend

import (
	"context"
	"encoding/json"

	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// Live-only events never change the retained-message cursor. Scope proofs use
// the same exclusive session and collect intervening events under its budget.
func mergeLiveEvents(ctx context.Context, client *irc.Client, page HistoryPage, buffer *watchBuffer) (HistoryPage, error) {
	r := buffer.request
	if r.Operation != core.EventsWatch {
		return page, nil
	}
	if buffer.known == nil {
		buffer.known = make(map[string]bool)
	}
	if r.ThreadMode == threadModeThread {
		// prepareSelector proved and canonicalized this root before OBSERVE.
		buffer.known[r.ParentID] = true
	}
	for _, message := range page.Messages {
		buffer.known[message.ID] = true
	}
	for i := 0; i < len(buffer.events); i++ {
		event := buffer.events[i]
		if r.ThreadMode == "" {
			continue
		}
		if event.Signal == nil || !validHistoryID(event.Signal.ID) {
			return HistoryPage{}, ErrReadUnavailable
		}
		id := event.Signal.ID
		if r.ThreadMode == threadModeReplies {
			if id != r.ParentID {
				return HistoryPage{}, ErrReadUnavailable
			}
			continue
		}
		if buffer.known[id] {
			continue
		}
		if buffer.proofs >= 128 {
			return HistoryPage{}, store.ErrCapacity
		}
		buffer.proofs++
		proof, err := boundedContextRequest(ctx, client, id, 1, core.MaxContextBytes, "room", buffer.collect)
		if err != nil {
			return HistoryPage{}, err
		}
		proofRequest := core.Request{Target: r.Target, ParentID: id}
		root, err := validateContextProof(proofRequest, proof)
		if err != nil || root != r.ParentID {
			return HistoryPage{}, ErrReadUnavailable
		}
		buffer.known[id] = true
	}
	// A scope proof may have collected messages while awaiting CHAT completion.
	var err error
	page, err = mergeWatch(page, buffer.messages, r)
	if err != nil {
		return HistoryPage{}, err
	}
	page.Events = append([]*LiveEvent(nil), buffer.events...)
	if len(page.Messages)+len(page.Events) > r.Limit {
		return HistoryPage{}, store.ErrCapacity
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded)+1 > MaxHistoryBytes {
		return HistoryPage{}, store.ErrCapacity
	}
	return page, nil
}

func freshWatchBuffer(r core.Request, client *irc.Client, previous *watchBuffer) *watchBuffer {
	return &watchBuffer{request: r, nick: client.Nick(), known: previous.known, proofs: previous.proofs}
}
