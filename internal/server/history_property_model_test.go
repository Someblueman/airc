package server

import "strings"

// A chronological list oracle. It has no ring offsets, cached mentions or ID
// indexes, and never calls production selection/eviction helpers.
type historyModel struct {
	messages []Message
	limit    int
	quotas   map[string]int
}

func (m *historyModel) trim() {
	counts := map[string]int{}
	kept := make([]Message, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= 0; i-- {
		v := m.messages[i]
		counts[v.Target]++
		if cap := m.quotas[v.Target]; cap == 0 || counts[v.Target] <= cap {
			kept = append(kept, v)
		}
	}
	m.messages = nil
	for i := len(kept) - 1; i >= 0; i-- {
		m.messages = append(m.messages, kept[i])
	}
}

func (m *historyModel) add(v Message) {
	if m.limit == 0 {
		return
	}
	counts := map[string]int{}
	for _, old := range m.messages {
		counts[old.Target]++
	}
	enabled := false
	for _, cap := range m.quotas {
		enabled = enabled || cap > 0
	}
	victim := -1
	if enabled {
		candidates := map[string]bool{}
		switch {
		case m.quotas[v.Target] > 0 && counts[v.Target] >= m.quotas[v.Target]:
			candidates[v.Target] = true
		case len(m.messages) == m.limit:
			counts[v.Target] += 0
			share := max(1, m.limit/len(counts))
			for target, n := range counts {
				if n > share {
					candidates[target] = true
				}
			}
			if len(candidates) == 0 {
				candidates[v.Target] = true
			}
		}
		for i, old := range m.messages {
			if candidates[old.Target] {
				victim = i
				break
			}
		}
	}
	if victim < 0 && len(m.messages) == m.limit {
		victim = 0
	}
	if victim >= 0 {
		m.messages = append(m.messages[:victim], m.messages[victim+1:]...)
	}
	for i := range m.messages {
		if m.messages[i].ID == v.Supersedes {
			m.messages[i].SupersededBy = v.ID
			m.messages[i].Retracted = v.Kind == "retract"
		}
	}
	m.messages = append(m.messages, v)
}

func modelMatches(v Message, target string) bool {
	switch {
	case strings.HasPrefix(target, "thread:"):
		id := strings.TrimPrefix(target, "thread:")
		return v.ID == id || v.ThreadID == id
	case strings.HasPrefix(target, "replies:"):
		return v.ReplyTo == strings.TrimPrefix(target, "replies:") && v.Reaction == ""
	case target == "@*":
		return !strings.HasPrefix(v.Target, "#")
	case strings.HasPrefix(target, "@"):
		nick := target[1:]
		// Generated bodies have a single unambiguous @worker mention.
		return strings.EqualFold(v.Target, nick) || strings.HasPrefix(v.Target, "#") && !strings.EqualFold(v.From, nick) && strings.Contains(strings.ToLower(v.Body), "@"+strings.ToLower(nick)+" ")
	case strings.HasPrefix(target, "#"):
		return v.Target == target
	default:
		return strings.EqualFold(v.Target, target)
	}
}

func modelSince(messages []Message, target, after string, limit int) ([]Message, string) {
	start, status := 0, "ok"
	recent := after == ""
	if after != "" && after != "*" {
		start = -1
		for i, v := range messages {
			if v.ID == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			start = 0
			recent = true
			status = "expired"
		}
	}
	var eligible []Message
	for _, v := range messages[start:] {
		if modelMatches(v, target) {
			eligible = append(eligible, v)
		}
	}
	if len(eligible) > limit {
		if recent {
			eligible = eligible[len(eligible)-limit:]
		} else {
			eligible = eligible[:limit]
			status = "more"
		}
	}
	return eligible, status
}
