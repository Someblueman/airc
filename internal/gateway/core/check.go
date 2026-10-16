package core

import (
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	// MaxCheckRooms is how many rooms, threads and inbox one check reads in
	// all, as one OBSERVE command can follow.
	MaxCheckRooms = 16
	// MaxCheckWaitSeconds bounds a check's wait for news. The carrier's own
	// request deadline may end it sooner.
	MaxCheckWaitSeconds = 20
	MaxCheckBytes       = 64 << 10
)

// CheckRoom is one room a check reads and where to read from: "recent"
// (the default), "retained", or a message ID to read after.
type CheckRoom struct {
	Name  string `json:"name"`
	After string `json:"after,omitempty"`
}

// CheckInbox asks a check for the author's direct messages after a position,
// as CheckRoom.After.
type CheckInbox struct {
	After string `json:"after,omitempty"`
}

// CheckThread asks a check for a followed thread (its root and every
// descendant) after a position, as CheckRoom.After.
type CheckThread struct {
	ID    string `json:"id"`
	After string `json:"after,omitempty"`
}

// CheckResult answers a check with one page per requested room, in request
// order, and the inbox page when asked for.
type CheckResult struct {
	Rooms []CheckPage `json:"rooms"`
	// Threads holds a page per requested thread, in request order; its Room
	// is "thread:" and the thread's ID.
	Threads []CheckPage `json:"threads,omitempty"`
	// Inbox holds mentions of the author in rooms it may read and, for a
	// registered author, direct messages AIRC recorded as received by its
	// account; its Room is "@" and the author's nickname.
	Inbox *CheckPage `json:"inbox,omitempty"`
}

// CheckPage is a room's news. Cursor is the message ID to pass as the
// room's next after; status "more" means more is already waiting, and gap
// marks a retention gap. A thread page may instead be "withheld" (it has
// news outside the rooms the author may read, such as direct messages) or
// "expired" (AIRC no longer retains its root), with no messages.
type CheckPage struct {
	Room string `json:"room"`
	// MentionsOnly marks an inbox page without direct messages: AIRC
	// cannot prove which account received them for a guest, or the listener
	// does not allow the author's own nickname. A client keeping one inbox
	// position must not move it past direct messages it was not shown.
	MentionsOnly bool                   `json:"mentions_only,omitempty"`
	Messages     []*irc.MessageMetadata `json:"messages"`
	Cursor       string                 `json:"cursor"`
	Status       string                 `json:"status"`
	Gap          bool                   `json:"gap"`
}

// normalizeCheck validates a check's rooms and wait, canonicalizing each
// room's start.
func (r *Request) normalizeCheck() error {
	targets := len(r.Rooms) + len(r.Threads)
	if r.Inbox != nil {
		targets++
	}
	if r.Target != nil || r.LookupID != "" || r.Cursor != "" || r.Query != "" || r.Start != "" || r.MaxBytes != 0 || r.Limit < 1 || r.Limit > 1000 || targets < 1 || targets > MaxCheckRooms || r.WaitSeconds < 0 || r.WaitSeconds > MaxCheckWaitSeconds {
		return errors.New("invalid check")
	}
	if r.Inbox != nil {
		inbox := *r.Inbox
		if inbox.After == "" {
			inbox.After = "recent"
		}
		if !ValidStart(inbox.After) {
			return errors.New("invalid inbox position")
		}
		r.Inbox = &inbox
	}
	rooms := make([]CheckRoom, len(r.Rooms))
	seen := make(map[string]bool, len(r.Rooms))
	for i, room := range r.Rooms {
		target := Target{Kind: "room", Name: room.Name}
		if err := target.Normalize(); err != nil || seen[target.Name] {
			return errors.New("invalid or repeated check room")
		}
		if room.After == "" {
			room.After = "recent"
		}
		if !ValidStart(room.After) {
			return errors.New("invalid check position")
		}
		seen[target.Name] = true
		rooms[i] = CheckRoom{Name: target.Name, After: room.After}
	}
	r.Rooms = rooms
	threads := make([]CheckThread, len(r.Threads))
	for i, thread := range r.Threads {
		if !protocol.ValidMessageID(thread.ID) || seen["thread:"+thread.ID] {
			return errors.New("invalid or repeated check thread")
		}
		if thread.After == "" {
			thread.After = "recent"
		}
		if !ValidStart(thread.After) {
			return errors.New("invalid check position")
		}
		seen["thread:"+thread.ID] = true
		threads[i] = thread
	}
	r.Threads = threads
	return nil
}
