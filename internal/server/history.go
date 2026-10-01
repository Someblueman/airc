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
}

func newHistory(limit int) historyRing {
	if limit < 0 {
		limit = 0
	}
	return historyRing{items: make([]Message, limit), mentions: make([][]string, limit), positions: make(map[string]int, limit), limit: limit}
}

func (h *historyRing) add(message Message) []string {
	var mentions []string
	if isChannelName(message.Target) {
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
		if previous := h.items[index].ID; h.positions[previous] == index {
			delete(h.positions, previous)
		}
		h.start = (h.start + 1) % h.limit
	}
	h.items[index], h.mentions[index] = message, mentions
	h.positions[message.ID] = index
	return mentions
}

func (h *historyRing) at(i int) Message { return h.items[(h.start+i)%h.limit] }

func isChannelName(target string) bool {
	return strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&")
}

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
		return !isChannelName(message.Target)
	}
	if isChannelName(target) {
		return message.Target == target
	}
	if nick, ok := strings.CutPrefix(target, "@"); ok {
		if strings.EqualFold(message.Target, nick) {
			return true
		}
		if !isChannelName(message.Target) || strings.EqualFold(message.From, nick) {
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
func (s *Server) recordLocked(message Message) []string {
	mentions := s.history.add(message)
	if s.histFile == nil {
		return mentions
	}
	file := s.histFile
	s.mu.Unlock()
	line, err := json.Marshal(message)
	if err == nil {
		_, err = file.Write(append(line, '\n'))
	}
	s.mu.Lock()
	if err != nil {
		s.persistenceError = err.Error()
		// Keep serving from memory; retrying a broken file would only spam the log.
		s.logger.Error("history_write_failed", "error", err.Error())
		if s.histFile == file {
			_ = file.Close()
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
	messages, dirty, err := readHistoryFile(path, s.cfg.HistoryLimit)
	if err != nil {
		return err
	}
	if dirty {
		if err := rewriteHistoryFile(path, messages); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	for _, message := range messages {
		s.history.add(message)
		if message.Seq > s.seq {
			s.seq = message.Seq
		}
	}
	s.histFile = file
	s.logger.Info("history_restored", "path", path, "messages", len(messages))
	return nil
}

// readHistoryFile returns the newest limit valid messages. dirty reports that
// the file holds more than that, or unreadable lines, and should be rewritten.
func readHistoryFile(path string, limit int) (messages []Message, dirty bool, err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open history file: %w", err)
	}
	defer file.Close()
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
		messages = append(messages, message)
		if len(messages) >= 2*limit {
			messages = append(messages[:0], messages[len(messages)-limit:]...)
			dirty = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("read history file: %w", err)
	}
	if len(messages) > limit {
		messages = append(messages[:0], messages[len(messages)-limit:]...)
		dirty = true
	}
	return messages, dirty, nil
}

func rewriteHistoryFile(path string, messages []Message) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".airc-history-*")
	if err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	defer os.Remove(temp.Name())
	writer := bufio.NewWriter(temp)
	for _, message := range messages {
		line, _ := json.Marshal(message)
		writer.Write(line)
		writer.WriteByte('\n')
	}
	if err := writer.Flush(); err != nil {
		temp.Close()
		return fmt.Errorf("compact history file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		return fmt.Errorf("compact history file: %w", err)
	}
	return os.Rename(temp.Name(), path)
}
