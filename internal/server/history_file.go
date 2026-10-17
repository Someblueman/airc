package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/internal/atomicfile"
)

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
	s.commit.init(file, s.cfg.Sync)
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
// Lines written by posts still waiting for their sync are synced first, so the
// replacement, which holds them too, never races a sync of the old file.
func (s *Server) compactHistoryLocked() error {
	s.mu.Unlock()
	err := s.commit.drain()
	s.mu.Lock()
	if err != nil {
		return err
	}
	messages := make([]Message, s.history.size)
	for i := range messages {
		messages[i] = s.history.at(i)
	}
	path, old := s.histPath, s.histFile
	s.mu.Unlock()
	err = rewriteHistoryFile(path, messages, s.cfg.Sync)
	var next *os.File
	if err == nil {
		next, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	}
	s.mu.Lock()
	if err != nil {
		return err
	}
	s.histFile = next
	s.commit.retire(old)
	s.commit.init(next, s.cfg.Sync)
	s.histRecords = len(messages)
	if info, err := next.Stat(); err == nil {
		s.histBytes = info.Size()
	}
	return syncHistoryDir(path, s.cfg.Sync) // the swapped descriptor is valid either way
}
