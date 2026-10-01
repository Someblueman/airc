package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// cursorStore remembers, per agent nickname and server, the ID of the last
// message already returned for each channel or direct-message inbox, so
// `airc check` can resume without the agent tracking anything itself.
type cursorStore struct {
	path       string
	lock       *os.File
	saved      []byte
	Cursors    map[string]string `json:"cursors"`
	ReplyOrder []string          `json:"reply_order,omitempty"`
	Follows    []string          `json:"follows,omitempty"`
	Pins       map[string]string `json:"pins,omitempty"`
	// Topics holds the last channel header shown to this agent, so a header is
	// shown once and again only when it changes.
	Topics map[string]string `json:"topics,omitempty"`
	Server string            `json:"server"`
	Nick   string            `json:"nick"`
}

func stateDir() (string, error) {
	if dir := os.Getenv("AIRC_STATE_DIR"); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "airc"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate state directory (set AIRC_STATE_DIR): %w", err)
	}
	return filepath.Join(home, ".local", "state", "airc"), nil
}

// openCursors loads the cursors for nick on the server in opt and takes an
// exclusive lock so two concurrent checks cannot read the same messages twice.
func openCursors(opt options, nick string) (*cursorStore, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	path, server, err := cursorPath(opt, nick)
	if err != nil {
		return nil, err
	}
	store := &cursorStore{path: path, Cursors: map[string]string{}, Topics: map[string]string{}, Server: server, Nick: nick}
	lock, err := os.OpenFile(store.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open cursor lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another airc check for %q is already running", nick)
		}
		return nil, fmt.Errorf("lock cursors: %w", err)
	}
	store.lock = lock
	owner, _ := json.Marshal(cursorOwner{PID: os.Getpid(), Since: time.Now().UTC()})
	if err := lock.Truncate(0); err != nil {
		store.close()
		return nil, fmt.Errorf("write cursor lock owner: %w", err)
	}
	if _, err := lock.Write(owner); err != nil {
		store.close()
		return nil, fmt.Errorf("write cursor lock owner: %w", err)
	}
	data, err := os.ReadFile(store.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		store.close()
		return nil, fmt.Errorf("read cursors: %w", err)
	default:
		if err := json.Unmarshal(data, store); err != nil {
			store.close()
			return nil, fmt.Errorf("cursor file %s is corrupt (delete it to start over): %w", store.path, err)
		}
		if store.Cursors == nil {
			store.Cursors = map[string]string{}
		}
		if store.Topics == nil {
			store.Topics = map[string]string{}
		}
		store.saved = data
	}
	return store, nil
}

func (s *cursorStore) save(cursors map[string]string) error {
	s.Cursors = cursors
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if bytes.Equal(data, s.saved) || (s.saved == nil && len(s.Cursors) == 0 && len(s.Topics) == 0 && len(s.Follows) == 0 && len(s.Pins) == 0) {
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".cursors-*")
	if err != nil {
		return fmt.Errorf("save cursors: %w", err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("save cursors: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("save cursors: %w", err)
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return fmt.Errorf("save cursors: %w", err)
	}
	s.saved = data
	return nil
}

func (s *cursorStore) close() {
	if s.lock != nil {
		s.lock.Close() // releases the flock
		s.lock = nil
	}
}

// Cache the most recent 64 reply checks, rather than retaining one cursor for
// every message an agent has ever asked about. Eviction permits replay only;
// it never advances a room or inbox cursor or skips a reply.
func (s *cursorStore) rememberReplyTarget(target string, cursors map[string]string) {
	for i, key := range s.ReplyOrder {
		if key == target {
			s.ReplyOrder = append(s.ReplyOrder[:i], s.ReplyOrder[i+1:]...)
			break
		}
	}
	s.ReplyOrder = append(s.ReplyOrder, target)
	if len(s.ReplyOrder) > 64 {
		delete(cursors, s.ReplyOrder[0])
		s.ReplyOrder = s.ReplyOrder[1:]
	}
}

func cursorPath(opt options, nick string) (string, string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", "", err
	}
	server := connectionKey(opt, "://")
	sum := sha256.Sum256([]byte(server + "\x00" + strings.ToLower(nick)))
	return filepath.Join(dir, "cursors-"+hex.EncodeToString(sum[:8])+".json"), server, nil
}

type cursorOwner struct {
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
}
