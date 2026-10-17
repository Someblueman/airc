package server

import (
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// History query statuses reported in the end-of-history numeric.
const (
	historyOK      = "ok"      // everything requested was returned
	historyMore    = "more"    // the limit was reached; more messages follow the last one returned
	historyExpired = "expired" // the cursor is not in the retained window; the latest messages were returned instead
)

// entryOverhead approximates the heap a retained message costs beyond the
// bytes of its strings: the entry and Message structs, its cursor, request
// and index entries. TestHistoryBytesTrackHeap checks it against the heap.
const entryOverhead = 440

// entry is a retained message with what is derived from it on insertion.
type entry struct {
	Message
	mentions []string // lower-case nicknames a channel message tags or addresses
	bytes    int
}

func (e *entry) size() int {
	m := &e.Message
	n := entryOverhead + 2*len(m.ID) + len(m.From) + len(m.Target) + len(m.Body) + len(m.ReplyTo) + 2*len(m.ThreadID) +
		len(m.Reaction) + len(m.AccountID) + len(m.RecipientAccountID) + 2*len(m.RequestID) + len(m.Kind) + len(m.Supersedes) + len(m.SupersededBy)
	for _, option := range m.PollOptions {
		n += 16 + len(option)
	}
	for _, nick := range e.mentions {
		n += 40 + 2*len(nick)
	}
	return n
}

// posList is an ascending list of positions with a movable front.
type posList struct {
	items []uint64
	head  int
}

func (l *posList) len() int        { return len(l.items) - l.head }
func (l *posList) at(i int) uint64 { return l.items[l.head+i] }
func (l *posList) push(p uint64)   { l.items = append(l.items, p) }
func (l *posList) search(p uint64) int { // the first index at or after position p
	return sort.Search(l.len(), func(i int) bool { return l.at(i) >= p })
}

func (l *posList) popFront() {
	l.head++
	if l.head >= 64 && 2*l.head >= len(l.items) {
		l.items = slices.Clone(l.items[l.head:])
		l.head = 0
	}
}

// historyRing is the in-memory history window: messages in arrival order,
// bounded by count and by approximate bytes, with an index per room, inbox,
// thread and parent so a read costs the page it returns rather than the
// window. Each message has a position that never changes; the oldest are
// evicted from the front, and room quotas may remove older ones from the
// middle, which leaves a hole until the front passes it.
type historyRing struct {
	entries   []*entry // entries[i] holds position base+i; nil once removed
	head      int      // entries before head are gone
	base      uint64
	size      int
	bytes     int64
	limit     int
	maxBytes  int64 // 0 means only limit bounds the window
	positions map[string]uint64
	requests  map[string]uint64
	index     map[string]*posList
	quotas    map[string]roomSettings
	// counts is the number of retained messages per target, kept in step with
	// every insert and removal so quota decisions need no scan of the window.
	counts map[string]int
}

func newHistory(limit int) historyRing { return newHistoryWindow(limit, 0) }

func newHistoryWindow(limit int, maxBytes int64) historyRing {
	return historyRing{limit: max(limit, 0), maxBytes: maxBytes, positions: map[string]uint64{}, requests: map[string]uint64{}, index: map[string]*posList{}, counts: map[string]int{}}
}

func (h *historyRing) next() uint64 { return h.base + uint64(len(h.entries)) }

// get returns the live entry at position p, or nil.
func (h *historyRing) get(p uint64) *entry {
	if p < h.base+uint64(h.head) || p >= h.next() {
		return nil
	}
	return h.entries[p-h.base]
}

func (h *historyRing) entryByID(id string) *entry {
	if p, ok := h.positions[id]; ok {
		return h.get(p)
	}
	return nil
}

func (h *historyRing) request(key string) *entry {
	if p, ok := h.requests[key]; ok {
		return h.get(p)
	}
	return nil
}

func (h *historyRing) message(id string) (Message, bool) {
	if e := h.entryByID(id); e != nil {
		return e.Message, true
	}
	return Message{}, false
}

// indexKeys names the index lists a message belongs to. The prefixes cannot
// collide with channel names, which start with # or &.
func indexKeys(m *Message, mentions []string, each func(string)) {
	if protocol.IsChannel(m.Target) {
		each(m.Target)
		for _, nick := range mentions {
			each("mention:" + nick)
		}
	} else {
		each("dm:" + nickKey(m.Target))
		each("dm:*")
	}
	if m.ThreadID != "" {
		each("thread:" + m.ThreadID)
	}
	if m.ReplyTo != "" {
		each("child:" + m.ReplyTo)
	}
}

func (h *historyRing) uncount(target string) {
	if h.counts[target] <= 1 {
		delete(h.counts, target)
	} else {
		h.counts[target]--
	}
}

func (h *historyRing) add(message Message) []string {
	var mentions []string
	if protocol.IsChannel(message.Target) {
		mentions = protocol.Mentions(message.Body)
	}
	if h.limit == 0 {
		return mentions
	}
	e := &entry{Message: message, mentions: mentions}
	e.bytes = e.size()
	h.makeRoom(message.Target, e.bytes)
	p := h.next()
	h.entries = append(h.entries, e)
	h.size++
	h.bytes += int64(e.bytes)
	h.counts[message.Target]++
	h.positions[message.ID] = p
	if message.Supersedes != "" {
		if original := h.entryByID(message.Supersedes); original != nil {
			original.SupersededBy = message.ID
			original.Retracted = message.Kind == "retract"
		}
	}
	if key := requestKey(message.From, message.AccountID, message.RequestID); key != "" {
		h.requests[key] = p
	}
	indexKeys(&e.Message, mentions, func(key string) {
		l := h.index[key]
		if l == nil {
			l = &posList{}
			h.index[key] = l
		}
		l.push(p)
	})
	for h.size > h.limit || h.maxBytes > 0 && h.bytes > h.maxBytes && h.size > 1 {
		h.remove(h.base + uint64(h.head))
	}
	return mentions
}

// remove drops the live message at position p.
func (h *historyRing) remove(p uint64) {
	e := h.get(p)
	if e == nil {
		return
	}
	h.entries[p-h.base] = nil
	h.size--
	h.bytes -= int64(e.bytes)
	h.uncount(e.Target)
	if h.positions[e.ID] == p {
		delete(h.positions, e.ID)
	}
	if key := requestKey(e.From, e.AccountID, e.RequestID); key != "" && h.requests[key] == p {
		delete(h.requests, key)
	}
	indexKeys(&e.Message, e.mentions, h.trim)
	for h.head < len(h.entries) && h.entries[h.head] == nil {
		h.head++
	}
	if h.head >= 1024 && 2*h.head >= len(h.entries) {
		h.base += uint64(h.head)
		h.entries = slices.Clone(h.entries[h.head:])
		h.head = 0
	}
}

// trim drops removed positions from the front of an index list, and the list
// once it is empty. A position removed from the middle waits for the front.
func (h *historyRing) trim(key string) {
	l := h.index[key]
	if l == nil {
		return
	}
	for l.len() > 0 && h.get(l.at(0)) == nil {
		l.popFront()
	}
	if l.len() == 0 {
		delete(h.index, key)
	}
}

// each visits live messages oldest first until visit returns false.
func (h *historyRing) each(visit func(*entry) bool) {
	for _, e := range h.entries[h.head:] {
		if e != nil && !visit(e) {
			return
		}
	}
}

// at returns the i-th live message, oldest first. It walks the window; tests
// use it, and production code iterates instead.
func (h *historyRing) at(i int) Message {
	var found Message
	h.each(func(e *entry) bool {
		if i == 0 {
			found = e.Message
			return false
		}
		i--
		return true
	})
	return found
}

// last returns the newest retained message.
func (h *historyRing) last() (Message, bool) {
	for i := len(h.entries) - 1; i >= h.head; i-- {
		if e := h.entries[i]; e != nil {
			return e.Message, true
		}
	}
	return Message{}, false
}

// selection is how a history target is read: the index lists holding its
// messages, a thread root kept outside them, and a filter for list entries
// that do not belong to the target. scan reads the whole window instead.
type selection struct {
	lists   []*posList
	root    uint64
	hasRoot bool
	keep    func(*entry) bool
	scan    bool
}

func (h *historyRing) lists(keys ...string) []*posList {
	var out []*posList
	for _, key := range keys {
		if l := h.index[key]; l != nil {
			out = append(out, l)
		}
	}
	return out
}

// selectTarget maps a target as matchesTarget defines it to its indexes.
// "*" selects everything.
func (h *historyRing) selectTarget(target string) selection {
	if target == "*" {
		return selection{scan: true}
	}
	if id, replies, ok := protocol.ConversationTarget(target); ok {
		if replies {
			return selection{lists: h.lists("child:" + id), keep: func(e *entry) bool { return e.Reaction == "" }}
		}
		s := selection{lists: h.lists("thread:" + id)}
		s.root, s.hasRoot = h.positions[id]
		return s
	}
	if target == protocol.AllDirectMessages {
		return selection{lists: h.lists("dm:*")}
	}
	if protocol.IsChannel(target) {
		return selection{lists: h.lists(target)}
	}
	if nick, ok := strings.CutPrefix(target, "@"); ok {
		key := nickKey(nick)
		return selection{lists: h.lists("dm:"+key, "mention:"+key), keep: func(e *entry) bool {
			return !protocol.IsChannel(e.Target) || !strings.EqualFold(e.From, nick)
		}}
	}
	return selection{lists: h.lists("dm:" + nickKey(target))}
}

// matchesTarget is the definition the indexes implement: channels compare
// exactly and nicknames case-insensitively. "@nick" selects what needs that
// agent's attention: direct messages to it, and channel messages from others
// that tag or address it.
func matchesTarget(e *entry, target string) bool {
	m := &e.Message
	if target == "*" {
		return true
	}
	if id, replies, ok := protocol.ConversationTarget(target); ok {
		if replies {
			return m.ReplyTo == id && m.Reaction == ""
		}
		return m.ID == id || m.ThreadID == id
	}
	if target == protocol.AllDirectMessages {
		return !protocol.IsChannel(m.Target)
	}
	if protocol.IsChannel(target) {
		return m.Target == target
	}
	if nick, ok := strings.CutPrefix(target, "@"); ok {
		if strings.EqualFold(m.Target, nick) {
			return true
		}
		return protocol.IsChannel(m.Target) && !strings.EqualFold(m.From, nick) && slices.Contains(e.mentions, nickKey(nick))
	}
	return strings.EqualFold(m.Target, target)
}

// forward visits the selected live messages at or after position from,
// oldest first, until visit returns false.
func (h *historyRing) forward(s selection, from uint64, visit func(uint64, *entry) bool) {
	if s.scan {
		start := max(from, h.base+uint64(h.head))
		for p := start; p < h.next(); p++ {
			if e := h.entries[p-h.base]; e != nil && !visit(p, e) {
				return
			}
		}
		return
	}
	cursors := make([]int, len(s.lists))
	for i, l := range s.lists {
		cursors[i] = l.search(from)
	}
	root := s.hasRoot && s.root >= from
	for {
		next, which := uint64(math.MaxUint64), -1
		for i, l := range s.lists {
			if cursors[i] < l.len() && l.at(cursors[i]) < next {
				next, which = l.at(cursors[i]), i
			}
		}
		if root && s.root <= next {
			next, which, root = s.root, -2, false
		}
		if which == -1 {
			return
		}
		for i, l := range s.lists { // a position in several lists is one message
			for cursors[i] < l.len() && l.at(cursors[i]) == next {
				cursors[i]++
			}
		}
		if e := h.get(next); e != nil && (s.keep == nil || s.keep(e)) && !visit(next, e) {
			return
		}
	}
}

// backward visits the selected live messages newest first until visit
// returns false.
func (h *historyRing) backward(s selection, visit func(uint64, *entry) bool) {
	if s.scan {
		for i := len(h.entries) - 1; i >= h.head; i-- {
			if e := h.entries[i]; e != nil && !visit(h.base+uint64(i), e) {
				return
			}
		}
		return
	}
	cursors := make([]int, len(s.lists))
	for i, l := range s.lists {
		cursors[i] = l.len() - 1
	}
	root := s.hasRoot
	for {
		next, which := uint64(0), -1
		for i, l := range s.lists {
			if cursors[i] >= 0 && (which == -1 || l.at(cursors[i]) > next) {
				next, which = l.at(cursors[i]), i
			}
		}
		if root && (which == -1 || s.root >= next) {
			next, which, root = s.root, -2, false
		}
		if which == -1 {
			return
		}
		for i, l := range s.lists {
			for cursors[i] >= 0 && l.at(cursors[i]) == next {
				cursors[i]--
			}
		}
		if e := h.get(next); e != nil && (s.keep == nil || s.keep(e)) && !visit(next, e) {
			return
		}
	}
}

func (h *historyRing) recent(target string, limit int) []Message {
	if limit <= 0 || limit > h.size {
		limit = h.size
	}
	var out []Message
	h.backward(h.selectTarget(target), func(_ uint64, e *entry) bool {
		if out == nil {
			out = make([]Message, 0, min(limit, 64))
		}
		out = append(out, e.Message)
		return len(out) < limit
	})
	slices.Reverse(out)
	return out
}

// start resolves a cursor to the position after it: "*" is the beginning.
func (h *historyRing) start(after string) (uint64, bool) {
	if after == "*" {
		return 0, true
	}
	p, ok := h.positions[after]
	return p + 1, ok
}

// since returns history for target. Without a cursor it returns the latest limit
// messages. With a cursor it returns the oldest limit messages after the cursor,
// so a caller that pages forward never skips anything. A cursor that has left
// the retained window cannot be resumed reliably, so the latest messages are
// returned with historyExpired and the caller decides what to do.
func (h *historyRing) since(target, after string, limit int) ([]Message, string) {
	if after == "" {
		return h.recent(target, limit), historyOK
	}
	from, found := h.start(after)
	if !found {
		return h.recent(target, limit), historyExpired
	}
	return h.page(h.selectTarget(target), from, limit, nil)
}

// page returns up to limit selected messages from position from, skipping
// those skip rejects, with historyMore when more follow.
func (h *historyRing) page(s selection, from uint64, limit int, skip func(*entry) bool) ([]Message, string) {
	if limit <= 0 {
		limit = h.size
	}
	var out []Message
	status := historyOK
	h.forward(s, from, func(_ uint64, e *entry) bool {
		if skip != nil && skip(e) {
			return true
		}
		if len(out) == limit {
			status = historyMore
			return false
		}
		out = append(out, e.Message)
		return true
	})
	return out, status
}

// full reports whether adding bytes more would evict.
func (h *historyRing) full(bytes int) bool {
	return h.size >= h.limit || h.maxBytes > 0 && h.bytes+int64(bytes) > h.maxBytes
}
