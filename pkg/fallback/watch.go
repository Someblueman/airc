package fallback

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// RoomWatch follows one room over a gateway carrier, one long poll at a time,
// so a client whose native IRC connection died keeps receiving the room. It
// starts from a message ID, so it continues exactly where native IRC left
// off, and Last says where native IRC (or another watch) should resume.
type RoomWatch struct {
	c     *Client
	k     *Conn
	room  string
	limit int
	// start is the first poll's position: a message ID, or "recent".
	start string
	// cursor is the gateway's own position once it has answered.
	cursor string
	last   string
}

// WatchPage is one poll's messages, oldest first.
type WatchPage struct {
	Endpoint Endpoint
	Messages []*irc.MessageMetadata
	// Gap is set when messages may be missing: the start had expired, so
	// the watch began at the oldest retained message.
	Gap bool
	// More is set when retained messages remain: poll again at once.
	More bool
}

// WatchRoom races the client's gateway carriers (never native IRC) and
// follows room on the first to connect, from after: a message ID such as a
// native IRC cursor or the last message seen, or "" for the latest limit
// messages.
func (c *Client) WatchRoom(ctx context.Context, room, after string, limit int) (*RoomWatch, error) {
	if !protocol.IsChannel(room) || limit < 1 || limit > 1000 || after != "" && !protocol.ValidMessageID(after) {
		return nil, errors.New("a watch needs a room, a limit of 1-1000 and a message ID or nothing to start after")
	}
	gateways := *c
	gateways.Endpoints = nil
	for _, e := range c.Endpoints {
		if e.Name != "irc" {
			gateways.Endpoints = append(gateways.Endpoints, e)
		}
	}
	k, err := gateways.Connect(ctx)
	if err != nil {
		return nil, err
	}
	w := &RoomWatch{c: c, k: k, room: room, limit: limit, start: after, last: after}
	if after == "" {
		w.start = "recent"
	}
	return w, nil
}

// Next returns the messages after the watch's position: retained ones at
// once, or else the next to arrive within about two seconds (an empty page
// when none did). An error means this carrier stopped answering; start a new
// watch from Last.
func (w *RoomWatch) Next(ctx context.Context) (*WatchPage, error) {
	r := request{RequestID: "watch", Operation: "watch", Target: targetOf(w.room), Limit: w.limit, Cursor: w.cursor}
	if w.cursor == "" {
		r.Start = w.start
	}
	d, err := w.c.call(ctx, w.k, r)
	if err != nil {
		return nil, err
	}
	if d.Page == nil || d.Page.Status != "ok" && d.Page.Status != "more" {
		return nil, errors.New("gateway answered a watch without a page")
	}
	for _, m := range d.Page.Messages {
		if m == nil || !protocol.ValidMessageID(m.ID) || m.Target != w.room {
			return nil, errors.New("gateway answered a watch with a malformed message")
		}
	}
	if d.Page.Cursor != "" {
		w.cursor = d.Page.Cursor
	}
	if n := len(d.Page.Messages); n > 0 {
		w.last = d.Page.Messages[n-1].ID
	}
	return &WatchPage{Endpoint: w.k.Endpoint, Messages: d.Page.Messages, Gap: d.Page.Gap, More: d.Page.Status == "more"}, nil
}

// Last is the newest message ID the watch returned, or its start when it has
// returned none ("" for the latest).
func (w *RoomWatch) Last() string { return w.last }

// Endpoint is the carrier the watch runs on.
func (w *RoomWatch) Endpoint() Endpoint { return w.k.Endpoint }

// Close closes the watch's connection.
func (w *RoomWatch) Close() error { return w.k.Close() }
