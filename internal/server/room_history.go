package server

// Quotas are opt-in. Default history retains the original O(1) ring behavior.
// With quotas, a bounded scan/compaction removes one selected old message while
// keeping all cursors, request IDs and mention indexes consistent.
func (h *historyRing) removeAt(position int) {
	old := h.at(position)
	h.uncount(old.Target)
	delete(h.positions, old.ID)
	delete(h.requests, requestKey(old.From, old.AccountID, old.RequestID))
	move := func(to, from int) {
		h.items[to], h.mentions[to] = h.items[from], h.mentions[from]
		h.positions[h.items[to].ID] = to
		if key := requestKey(h.items[to].From, h.items[to].AccountID, h.items[to].RequestID); key != "" {
			h.requests[key] = to
		}
	}
	// Close the gap from whichever end is nearer. Quota victims are old
	// messages, so this is usually a short shift at the front.
	if position < h.size-1-position {
		for i := position; i > 0; i-- {
			move((h.start+i)%h.limit, (h.start+i-1)%h.limit)
		}
		h.items[h.start], h.mentions[h.start] = Message{}, nil
		h.start = (h.start + 1) % h.limit
	} else {
		for i := position; i < h.size-1; i++ {
			move((h.start+i)%h.limit, (h.start+i+1)%h.limit)
		}
		last := (h.start + h.size - 1) % h.limit
		h.items[last], h.mentions[last] = Message{}, nil
	}
	h.size--
}

func (h *historyRing) quotaEnabled() bool {
	for _, r := range h.quotas {
		if r.HistoryLimit > 0 {
			return true
		}
	}
	return false
}

func (h *historyRing) makeRoom(target string) {
	if h.limit == 0 || !h.quotaEnabled() {
		return
	}
	// removeOldest drops the oldest retained message that matches.
	removeOldest := func(matches func(target string) bool) bool {
		for i := 0; i < h.size; i++ {
			if matches(h.items[(h.start+i)%h.limit].Target) {
				h.removeAt(i)
				return true
			}
		}
		return false
	}
	own := func(candidate string) bool { return candidate == target }
	if cap := h.quotas[target].HistoryLimit; cap > 0 && h.counts[target] >= cap {
		removeOldest(own)
		return
	}
	if h.size < h.limit {
		return
	}
	rooms := len(h.counts)
	if h.counts[target] == 0 {
		rooms++
	}
	share := max(1, h.limit/rooms)
	// Choose the oldest message belonging to a room above its fair share.
	if removeOldest(func(candidate string) bool { return h.counts[candidate] > share }) {
		return
	}
	// At equilibrium, the sender replaces its own oldest message.
	removeOldest(own)
	// A new room at the global bound replaces the globally oldest message.
}

func (h *historyRing) trimQuotas() {
	for target, r := range h.quotas {
		if r.HistoryLimit == 0 {
			continue
		}
		count := 0
		for i := h.size - 1; i >= 0; i-- {
			if h.at(i).Target == target {
				count++
				if count > r.HistoryLimit {
					h.removeAt(i)
				}
			}
		}
	}
}
