package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

// History query statuses reported in the end-of-history numeric.
const (
	historyOK      = "ok"      // everything requested was returned
	historyMore    = "more"    // the limit was reached; more messages follow the last one returned
	historyExpired = "expired" // the cursor is not in the retained window; the latest messages were returned instead
)

type historyRing struct {
	items     []Message
	start     int
	size      int
	limit     int
	positions map[string]int
	mentions  [][]string
	requests  map[string]int
	quotas    map[string]roomSettings
	// counts is the number of retained messages per target, kept in step with
	// every insert and removal so quota decisions need no scan of the ring.
	counts map[string]int
}

func (h *historyRing) uncount(target string) {
	if h.counts[target] <= 1 {
		delete(h.counts, target)
	} else {
		h.counts[target]--
	}
}

func newHistory(limit int) historyRing {
	if limit < 0 {
		limit = 0
	}
	return historyRing{items: make([]Message, limit), mentions: make([][]string, limit), positions: make(map[string]int, limit), requests: make(map[string]int), counts: make(map[string]int), limit: limit}
}

func (h *historyRing) add(message Message) []string {
	h.makeRoom(message.Target)
	var mentions []string
	if protocol.IsChannel(message.Target) {
		mentions = protocol.Mentions(message.Body)
	}
	if h.limit == 0 {
		return mentions
	}
	index := h.start
	if h.size < h.limit {
		index = (h.start + h.size) % h.limit
		h.size++
	} else {
		old := h.items[index]
		h.uncount(old.Target)
		delete(h.requests, requestKey(old.From, old.AccountID, old.RequestID))
		if previous := h.items[index].ID; h.positions[previous] == index {
			delete(h.positions, previous)
		}
		h.start = (h.start + 1) % h.limit
	}
	h.items[index], h.mentions[index] = message, mentions
	h.counts[message.Target]++
	h.positions[message.ID] = index
	if original, found := h.positions[message.Supersedes]; message.Supersedes != "" && found {
		h.items[original].SupersededBy = message.ID
		h.items[original].Retracted = message.Kind == "retract"
	}
	if key := requestKey(message.From, message.AccountID, message.RequestID); key != "" {
		h.requests[key] = index
	}
	return mentions
}

func (h *historyRing) at(i int) Message { return h.items[(h.start+i)%h.limit] }

// matchesAt compares channels exactly and nicknames case-insensitively.
// "@nick" selects what needs that agent's attention: direct messages to it, and
// channel messages from others that tag or address it.
func (h *historyRing) matchesAt(i int, target string) bool {
	index := (h.start + i) % h.limit
	message := h.items[index]
	if id, replies, ok := protocol.ConversationTarget(target); ok {
		if replies {
			return message.ReplyTo == id && message.Reaction == ""
		}
		return message.ID == id || message.ThreadID == id
	}
	if target == protocol.AllDirectMessages {
		return !protocol.IsChannel(message.Target)
	}
	if protocol.IsChannel(target) {
		return message.Target == target
	}
	if nick, ok := strings.CutPrefix(target, "@"); ok {
		if strings.EqualFold(message.Target, nick) {
			return true
		}
		if !protocol.IsChannel(message.Target) || strings.EqualFold(message.From, nick) {
			return false
		}
		for _, mention := range h.mentions[index] {
			if strings.EqualFold(mention, nick) {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(message.Target, target)
}

func (h *historyRing) recent(target string, limit int) []Message {
	if limit <= 0 || limit > h.size {
		limit = h.size
	}
	out := make([]Message, 0, limit)
	for i := h.size - 1; i >= 0 && len(out) < limit; i-- {
		if h.matchesAt(i, target) {
			out = append(out, h.at(i))
		}
	}
	slices.Reverse(out)
	return out
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
	cursor := -1
	if after != "*" {
		if index, found := h.positions[after]; found {
			cursor = (index - h.start + h.limit) % h.limit
		}
	}
	if cursor < 0 && after != "*" {
		return h.recent(target, limit), historyExpired
	}
	if limit <= 0 {
		limit = h.size
	}
	out := make([]Message, 0, min(limit, h.size-cursor-1))
	for i := cursor + 1; i < h.size; i++ {
		message := h.at(i)
		if !h.matchesAt(i, target) {
			continue
		}
		if len(out) == limit {
			return out, historyMore
		}
		out = append(out, message)
	}
	return out, historyOK
}

// recordLocked holds mu on entry and return, but releases it for file I/O.
// Message handlers also hold messageMu so appends, broadcasts and receipts
// retain their order while queries and connection cleanup can proceed.
func (s *Server) recordLocked(message *Message) []string {
	mentions := s.history.add(*message)
	if s.histFile == nil {
		return mentions
	}
	if s.messageMu.TryLock() {
		// A posting command missing from serializesPosts would interleave
		// appends with compaction; fail that one command loudly instead.
		s.messageMu.Unlock()
		panic("recordLocked called without messageMu")
	}
	file := s.histFile
	s.mu.Unlock()
	line, err := json.Marshal(message)
	if err == nil {
		_, err = file.Write(append(line, '\n'))
		if err == nil {
			err = atomicfile.File(file, s.cfg.Sync)
		}
	}
	s.mu.Lock()
	if err == nil {
		s.histBytes += int64(len(line) + 1)
		s.histRecords++
		message.Persisted = true
		if index, ok := s.history.positions[message.ID]; ok {
			s.history.items[index].Persisted = true
		}
	}
	if err == nil && s.histRecords >= s.histCompactAfter && (s.histRecords >= max(2, 2*s.history.limit) || s.histBytes > int64(max(1<<20, s.history.limit*32768))) {
		// A failed compaction leaves a valid descriptor: keep appending and
		// try again later, so a passing fault does not switch durability off.
		if compactErr := s.compactHistoryLocked(); compactErr != nil {
			s.persistenceError = compactErr.Error()
			s.histCompactAfter = s.histRecords + max(16, s.history.limit/4)
			s.logger.Error("history_compaction_failed", "error", compactErr.Error(), "retry_after_records", s.histCompactAfter)
		} else {
			s.persistenceError, s.histCompactAfter = "", 0
		}
	}
	if err != nil {
		s.persistenceError = err.Error()
		// Keep serving from memory; retrying a broken file would only spam the log.
		s.logger.Error("history_write_failed", "error", err.Error())
		if s.histFile != nil {
			_ = s.histFile.Close()
			s.histFile = nil
		}
	}
	return mentions
}

// RestoreHistory loads the newest retained messages from path and appends every
// later message to it, so history survives a daemon restart. It must be called
// before Serve. The file is JSON lines and is compacted to the retention limit
// when it is loaded.
func (s *Server) RestoreHistory(path string) error {
	if s.cfg.HistoryLimit == 0 {
		return errors.New("a history file requires a non-zero history limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.histFile != nil {
		return errors.New("history file is already open")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	messages, dirty, err := readHistoryFile(path, s.cfg.HistoryLimit, s.chat.Rooms)
	if err != nil {
		return err
	}
	if dirty {
		if err := rewriteHistoryFile(path, messages, s.cfg.Sync); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	if err := errors.Join(atomicfile.File(file, s.cfg.Sync), syncHistoryDir(path, s.cfg.Sync)); err != nil {
		file.Close()
		return fmt.Errorf("sync restored history: %w", err)
	}
	for _, message := range messages {
		message.Persisted = true
		s.history.add(message)
		if message.Seq > s.seq {
			s.seq = message.Seq
		}
	}
	s.histFile = file
	s.histPath = path
	s.histRecords = len(messages)
	if info, err := file.Stat(); err == nil {
		s.histBytes = info.Size()
	}
	s.logger.Info("history_restored", "path", path, "messages", len(messages))
	return nil
}

// readHistoryFile applies the current bounded eviction policy while streaming
// the archive. dirty reports discarded or unreadable lines to compact away.
func readHistoryFile(path string, limit int, quotas map[string]roomSettings) (messages []Message, dirty bool, err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open history file: %w", err)
	}
	defer file.Close()
	ring := newHistory(limit)
	ring.quotas = quotas
	seen := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var message Message
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message.ID == "" || message.Target == "" {
			dirty = true
			continue
		}
		ring.add(message)
		seen++
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("read history file: %w", err)
	}
	for i := 0; i < ring.size; i++ {
		messages = append(messages, ring.at(i))
	}
	// A crash can leave the last record without its newline; the next append
	// would then share its line and both would be unreadable after a restart.
	if info, err := file.Stat(); err == nil && info.Size() > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], info.Size()-1); err != nil || last[0] != '\n' {
			dirty = true
		}
	}
	dirty = dirty || seen > ring.size
	return messages, dirty, nil
}

func rewriteHistoryFile(path string, messages []Message, mode atomicfile.Sync) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".airc-history-*")
	if err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	defer os.Remove(temp.Name())
	writer := bufio.NewWriter(temp)
	for _, message := range messages {
		line, err := json.Marshal(message)
		if err != nil {
			temp.Close()
			return fmt.Errorf("compact history file: %w", err)
		}
		_, _ = writer.Write(line) // Flush reports any write error
		_ = writer.WriteByte('\n')
	}
	if err := writer.Flush(); err != nil {
		temp.Close()
		return fmt.Errorf("compact history file: %w", err)
	}
	if err := atomicfile.File(temp, mode); err != nil {
		temp.Close()
		return fmt.Errorf("sync compacted history: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	return nil
}

// Later appends go to the replacement inode, so with full durability its
// directory entry must reach the disk too.
func syncHistoryDir(path string, mode atomicfile.Sync) error {
	if mode != atomicfile.SyncFull {
		return nil
	}
	if err := atomicfile.Dir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync history directory: %w", err)
	}
	return nil
}

// messageMu excludes concurrent appends while the replacement is synced. mu
// remains available to readers; the old descriptor stays open until replacement.
func (s *Server) compactHistoryLocked() error {
	messages := make([]Message, s.history.size)
	for i := range messages {
		messages[i] = s.history.at(i)
	}
	path, old := s.histPath, s.histFile
	s.mu.Unlock()
	err := rewriteHistoryFile(path, messages, s.cfg.Sync)
	var next *os.File
	if err == nil {
		next, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	}
	s.mu.Lock()
	if err != nil {
		return err
	}
	s.histFile = next
	_ = old.Close()
	s.histRecords = len(messages)
	if info, err := next.Stat(); err == nil {
		s.histBytes = info.Size()
	}
	return syncHistoryDir(path, s.cfg.Sync) // the swapped descriptor is valid either way
}
