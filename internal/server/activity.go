package server

import (
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
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
	// doneStatusKeep is how long a done status stays on a nickname's card;
	// a working or blocked one stays until the next status, or a restart.
	doneStatusKeep = 24 * time.Hour
)

type activity struct {
	nick   string
	seen   time.Time
	rooms  map[string]time.Time
	status *protocol.AgentStatus // the latest status post, nil without one
}

// noteStatusLocked records nick's latest status post, on an activity record
// of its own if nick has none, since a status outlives the activity window.
func (st *directoryState) noteStatusLocked(nick string, status protocol.AgentStatus) {
	st.noteActivityLocked(nick, status.Target)
	if a := st.activity[nickKey(nick)]; a != nil {
		a.status = &status
	}
}

// agentStatusLocked is nick's latest status, nil without one or once a done
// status is a day old.
func (st *directoryState) agentStatusLocked(nick string, now time.Time) *protocol.AgentStatus {
	a := st.activity[nickKey(nick)]
	if a == nil || a.status == nil || staleStatus(a.status, now) {
		return nil
	}
	return a.status
}

func staleStatus(status *protocol.AgentStatus, now time.Time) bool {
	return status.State == "done" && now.Sub(status.At) >= doneStatusKeep
}

// noteActivityLocked records nick as active now, in rooms.
func (st *directoryState) noteActivityLocked(nick string, rooms ...string) {
	now := st.env.now().UTC()
	key := nickKey(nick)
	a := st.activity[key]
	if a == nil {
		if len(st.activity) >= maxActivity {
			st.pruneActivityLocked(now)
		}
		for len(st.activity) >= maxActivity {
			oldest := ""
			for k, other := range st.activity {
				if oldest == "" || other.seen.Before(st.activity[oldest].seen) {
					oldest = k
				}
			}
			delete(st.activity, oldest)
		}
		a = &activity{rooms: map[string]time.Time{}}
		st.activity[key] = a
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

// pruneActivityLocked drops records outside the activity window, keeping
// one whose status still stands; a done status a day old is dropped first.
func (st *directoryState) pruneActivityLocked(now time.Time) {
	for key, a := range st.activity {
		if a.status != nil && staleStatus(a.status, now) {
			a.status = nil
		}
		if now.Sub(a.seen) >= activeWindow && a.status == nil {
			delete(st.activity, key)
		}
	}
}

// recentLocked reports when nick was last active and the rooms it used,
// sorted, if that was within activeWindow.
func (st *directoryState) recentLocked(nick string, now time.Time) (time.Time, []string, bool) {
	a := st.activity[nickKey(nick)]
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
