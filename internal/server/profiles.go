package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// RestoreProfiles loads explicit profiles before Serve. Activity states are
// intentionally ephemeral: a restart cannot confirm that an agent is still thinking.
func (s *Server) RestoreProfiles(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profilesAt != "" {
		return errors.New("profiles file already configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	loaded := map[string]protocol.AgentCard{}
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		if err != nil {
			return err
		}
		if len(data) > 4<<20 {
			return errors.New("profiles file exceeds 4 MiB")
		}
		if err := json.Unmarshal(data, &loaded); err != nil {
			return fmt.Errorf("read profiles: %w", err)
		}
		if loaded == nil {
			return errors.New("profiles file must contain a JSON object")
		}
	}
	if len(loaded) > maxDirectoryCards {
		return errors.New("too many saved profiles")
	}
	for key, card := range loaded {
		if key != nickKey(card.Nick) || !validNick(card.Nick) || !protocol.BriefText(card.Model, 400) || !protocol.BriefText(card.Workspace, 400) || !protocol.BriefText(card.Tools, 400) || !protocol.BriefText(card.About, 400) {
			return errors.New("invalid saved profile")
		}
		card.State, card.Note, card.ExpiresAt, card.Connected = "unknown", "", time.Time{}, false
		loaded[key] = card
	}
	for i := 0; i < s.history.size; i++ {
		message := s.history.at(i)
		key := nickKey(message.From)
		if card, ok := loaded[key]; ok && message.Timestamp.After(card.LastSeen) {
			card.LastSeen = message.Timestamp
			loaded[key] = card
		}
	}
	s.directory, s.profilesAt = loaded, path
	return nil
}

func (s *Server) saveProfileLocked(key string, next protocol.AgentCard) error {
	if s.profilesAt == "" {
		return nil
	}
	profiles := make(map[string]protocol.AgentCard)
	for name, card := range s.directory {
		if name == key {
			continue
		}
		if card.AgentProfile != (protocol.AgentProfile{}) {
			card.State, card.Note, card.ExpiresAt, card.Connected = "unknown", "", time.Time{}, false
			profiles[name] = card
		}
	}
	if next.AgentProfile != (protocol.AgentProfile{}) {
		next.State, next.Note, next.ExpiresAt, next.Connected = "unknown", "", time.Time{}, false
		profiles[key] = next
	}
	data, err := json.Marshal(profiles)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.profilesAt), ".airc-profiles-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(append(data, '\n'))
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return err
	}
	return os.Rename(temp.Name(), s.profilesAt)
}

func (s *Server) touchCardLocked(nick string) {
	key := strings.ToLower(nick)
	if card, exists := s.directory[key]; exists {
		card.LastSeen = time.Now().UTC()
		s.directory[key] = card
	}
}
