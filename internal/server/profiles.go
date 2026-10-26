package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

// directoryState is the directory: profiles, which are persisted, and
// presence cards and one-shot activity, which are not. Every field is read
// and written with s.mu held; profile saves run under s.mu with env.soft.
type directoryState struct {
	directory  map[string]protocol.AgentCard
	profilesAt string
	activity   map[string]*activity // by nickname; see activity.go
	env        *storeEnv
}

var profilesSnapshot = snapshot{limit: 4 << 20, tooLarge: "profiles file exceeds 4 MiB"}

// RestoreProfiles loads explicit profiles before Serve. Activity states are
// intentionally ephemeral: a restart cannot confirm that an agent is still thinking.
func (s *Server) RestoreProfiles(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.profilesAt != "" {
		return errors.New("profiles file already configured")
	}
	if err := s.directoryState.load(path); err != nil {
		return err
	}
	s.refreshLastSeen(&s.history)
	return nil
}

func (st *directoryState) load(path string) error {
	data, found, err := profilesSnapshot.bytes(path)
	if err != nil {
		return err
	}
	loaded := map[string]protocol.AgentCard{}
	if found {
		if err := json.Unmarshal(data, &loaded); err != nil || loaded == nil {
			// Profiles are self-reported and republished by agents: keep the
			// evidence and start empty rather than refuse to start.
			bad, moveErr := atomicfile.SetAside(path)
			if moveErr != nil {
				return fmt.Errorf("read profiles: %w", errors.Join(err, moveErr))
			}
			st.env.logger.Error("profiles_file_corrupt", "path", path, "moved_to", bad)
			loaded = map[string]protocol.AgentCard{}
		}
	}
	if len(loaded) > maxDirectoryCards {
		return errors.New("too many saved profiles")
	}
	for key, card := range loaded {
		if key != nickKey(card.Nick) || !protocol.ValidNick(card.Nick) || !protocol.BriefText(card.Model, 400) || !protocol.BriefText(card.Workspace, 400) || !protocol.BriefText(card.Tools, 400) || !protocol.BriefText(card.About, 400) || !protocol.ValidKind(card.Kind) {
			return errors.New("invalid saved profile")
		}
		card.State, card.Note, card.ExpiresAt, card.Connected = "unknown", "", time.Time{}, false
		loaded[key] = card
	}
	st.directory, st.profilesAt = loaded, path
	return nil
}

// refreshLastSeen moves each saved profile's last-seen time forward to its
// newest retained message.
func (st *directoryState) refreshLastSeen(h *historyRing) {
	h.each(func(e *entry) bool {
		key := nickKey(e.From)
		if card, ok := st.directory[key]; ok && e.Timestamp.After(card.LastSeen) {
			card.LastSeen = e.Timestamp
			st.directory[key] = card
		}
		return true
	})
}

func (st *directoryState) close() error { return nil }

func (st *directoryState) saveProfileLocked(key string, next protocol.AgentCard) error {
	if st.profilesAt == "" {
		return nil
	}
	profiles := make(map[string]protocol.AgentCard)
	for name, card := range st.directory {
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
	return profilesSnapshot.write(st.profilesAt, profiles, st.env.soft)
}

func (st *directoryState) touchCardLocked(nick string) {
	key := strings.ToLower(nick)
	if card, exists := st.directory[key]; exists {
		card.LastSeen = st.env.now().UTC()
		st.directory[key] = card
	}
}

func (st *directoryState) roomForCardLocked(key string, now time.Time) bool {
	if _, exists := st.directory[key]; exists {
		return true
	}
	// Expired presence without a profile is disposable; profiles stay until cleared.
	for other, card := range st.directory {
		if card.AgentProfile == (protocol.AgentProfile{}) && !now.Before(card.ExpiresAt) {
			delete(st.directory, other)
		}
	}
	return len(st.directory) < maxDirectoryCards
}
