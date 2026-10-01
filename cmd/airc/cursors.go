package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// cursorStore remembers, per agent nickname and server, the ID of the last
// message already returned for each channel or direct-message inbox, so
// `airc check` can resume without the agent tracking anything itself.
type cursorStore struct {
	path    string
	lock    *os.File
	Cursors map[string]string `json:"cursors"`
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
	config := clientConfig(opt)
	server := config.Network + "://" + config.Addr
	sum := sha256.Sum256([]byte(server + "\x00" + strings.ToLower(nick)))
	store := &cursorStore{path: filepath.Join(dir, "cursors-"+hex.EncodeToString(sum[:8])+".json"), Cursors: map[string]string{}, Topics: map[string]string{}, Server: server, Nick: nick}
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
	}
	return store, nil
}

func (s *cursorStore) save(cursors map[string]string) error {
	s.Cursors = cursors
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".cursors-*")
	if err != nil {
		return fmt.Errorf("save cursors: %w", err)
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return fmt.Errorf("save cursors: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("save cursors: %w", err)
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return fmt.Errorf("save cursors: %w", err)
	}
	return nil
}

func (s *cursorStore) close() {
	if s.lock != nil {
		s.lock.Close() // releases the flock
		s.lock = nil
	}
}
