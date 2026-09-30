package server

type historyRing struct {
	items []Message
	start int
	size  int
	limit int
}

func newHistory(limit int) historyRing {
	if limit < 0 {
		limit = 0
	}
	return historyRing{items: make([]Message, limit), limit: limit}
}

func (h *historyRing) add(message Message) {
	if h.limit == 0 {
		return
	}
	if h.size < h.limit {
		index := (h.start + h.size) % h.limit
		h.items[index] = message
		h.size++
		return
	}
	h.items[h.start] = message
	h.start = (h.start + 1) % h.limit
}

func (h *historyRing) recent(target string, limit int) []Message {
	if limit <= 0 || limit > h.size {
		limit = h.size
	}
	out := make([]Message, 0, limit)
	for i := 0; i < h.size; i++ {
		message := h.items[(h.start+i)%h.limit]
		if message.Target == target {
			out = append(out, message)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
