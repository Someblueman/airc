package server

import (
	"math"

	"github.com/Someblueman/airc/internal/protocol"
)

// Quotas are opt-in. Without them the window only drops its oldest messages.
// With them, a room at its quota, or one above its fair share of a full
// window, gives up its own oldest message, found through its index.

func (h *historyRing) quotaEnabled() bool {
	for _, r := range h.quotas {
		if r.HistoryLimit > 0 {
			return true
		}
	}
	return false
}

// oldestOf returns the position of target's oldest retained message.
func (h *historyRing) oldestOf(target string) (uint64, bool) {
	key := target
	if !protocol.IsChannel(target) {
		key = "dm:" + nickKey(target) // shared by spellings of one nickname
	}
	l := h.index[key]
	if l == nil {
		return 0, false
	}
	for i := range l.len() {
		if e := h.get(l.at(i)); e != nil && e.Target == target {
			return l.at(i), true
		}
	}
	return 0, false
}

func (h *historyRing) makeRoom(target string, bytes int) {
	if h.limit == 0 || !h.quotaEnabled() {
		return
	}
	if cap := h.quotas[target].HistoryLimit; cap > 0 && h.counts[target] >= cap {
		if p, ok := h.oldestOf(target); ok {
			h.remove(p)
		}
		return
	}
	if !h.full(bytes) {
		return
	}
	rooms := len(h.counts)
	if h.counts[target] == 0 {
		rooms++
	}
	share := max(1, h.size/rooms)
	// Choose the oldest message belonging to a room above its fair share.
	oldest, found := uint64(math.MaxUint64), false
	for candidate, count := range h.counts {
		if count <= share {
			continue
		}
		if p, ok := h.oldestOf(candidate); ok && p < oldest {
			oldest, found = p, true
		}
	}
	if found {
		h.remove(oldest)
		return
	}
	// At equilibrium, the sender replaces its own oldest message.
	if p, ok := h.oldestOf(target); ok {
		h.remove(p)
	}
	// A new room at the global bound replaces the globally oldest message.
}

func (h *historyRing) trimQuotas() {
	for target, r := range h.quotas {
		for r.HistoryLimit > 0 && h.counts[target] > r.HistoryLimit {
			p, ok := h.oldestOf(target)
			if !ok {
				break
			}
			h.remove(p)
		}
	}
}
