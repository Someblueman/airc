package server

import (
	"slices"
	"time"
)

// One-shot agents hold no connection between commands, so the daemon would
// otherwise never know they are about. Each check, wait and post is noted
// here with its room, and agents listings, directory cards and direct-message
// receipts report the nicknames active within activeWindow. Nothing is
// persisted: after a restart an agent shows again with its next command.
// Anonymous queries (history, pins, listings) are not activity, so the
// throwaway nicknames they use never appear.
const (
	activeWindow     = 10 * time.Minute
	maxActivity      = 4096
	maxActivityRooms = 16
)

type activity struct {
	nick  string
	seen  time.Time
	rooms map[string]time.Time
}

// noteActivityLocked records nick as active now, in rooms.
func (s *Server) noteActivityLocked(nick string, rooms ...string) {
	now := s.now().UTC()
	key := nickKey(nick)
	a := s.activity[key]
	if a == nil {
		if len(s.activity) >= maxActivity {
			s.pruneActivityLocked(now)
		}
		for len(s.activity) >= maxActivity {
			oldest := ""
			for k, other := range s.activity {
				if oldest == "" || other.seen.Before(s.activity[oldest].seen) {
					oldest = k
				}
			}
			delete(s.activity, oldest)
		}
		a = &activity{rooms: map[string]time.Time{}}
		s.activity[key] = a
	}
	a.nick, a.seen = nick, now
	for _, room := range rooms {
		if validChannel(room) {
			a.rooms[room] = now
		}
	}
	for len(a.rooms) > maxActivityRooms {
		oldest := ""
		for room, at := range a.rooms {
			if oldest == "" || at.Before(a.rooms[oldest]) {
				oldest = room
			}
		}
		delete(a.rooms, oldest)
	}
}

func (s *Server) pruneActivityLocked(now time.Time) {
	for key, a := range s.activity {
		if now.Sub(a.seen) >= activeWindow {
			delete(s.activity, key)
		}
	}
}

// recentLocked reports when nick was last active and the rooms it used,
// sorted, if that was within activeWindow.
func (s *Server) recentLocked(nick string, now time.Time) (time.Time, []string, bool) {
	a := s.activity[nickKey(nick)]
	if a == nil || now.Sub(a.seen) >= activeWindow {
		return time.Time{}, nil, false
	}
	var rooms []string
	for room, at := range a.rooms {
		if now.Sub(at) < activeWindow {
			rooms = append(rooms, room)
		}
	}
	slices.Sort(rooms)
	return a.seen, rooms, true
}
