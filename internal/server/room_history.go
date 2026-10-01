package server

// Quotas are opt-in. Default history retains the original O(1) ring behavior.
// With quotas, a bounded scan/compaction removes one selected old message while
// keeping all cursors, request IDs and mention indexes consistent.
func (h *historyRing) removeAt(position int) {
	old := h.at(position)
	delete(h.positions, old.ID)
	delete(h.requests, requestKey(old.From, old.AccountID, old.RequestID))
	for i := position; i < h.size-1; i++ {
		to, from := (h.start+i)%h.limit, (h.start+i+1)%h.limit
		h.items[to], h.mentions[to] = h.items[from], h.mentions[from]
		h.positions[h.items[to].ID] = to
		if key := requestKey(h.items[to].From, h.items[to].AccountID, h.items[to].RequestID); key != "" {
			h.requests[key] = to
		}
	}
	last := (h.start + h.size - 1) % h.limit
	h.items[last], h.mentions[last] = Message{}, nil
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
	counts := map[string]int{}
	for i := 0; i < h.size; i++ {
		counts[h.at(i).Target]++
	}
	if cap := h.quotas[target].HistoryLimit; cap > 0 && counts[target] >= cap {
		for i := 0; i < h.size; i++ {
			if h.at(i).Target == target {
				h.removeAt(i)
				return
			}
		}
	}
	if h.size < h.limit {
		return
	}
	if _, exists := counts[target]; !exists {
		counts[target] = 0
	}
	share := max(1, h.limit/len(counts))
	// Choose the oldest message belonging to a room above its fair share.
	for i := 0; i < h.size; i++ {
		if counts[h.at(i).Target] > share {
			h.removeAt(i)
			return
		}
	}
	// At equilibrium, the sender replaces its own oldest message.
	for i := 0; i < h.size; i++ {
		if h.at(i).Target == target {
			h.removeAt(i)
			return
		}
	}
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
