package fallback

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Gateways is a copy of c limited to its gateway carriers, for reads that
// continue where native IRC left off while it is unreachable.
func (c *Client) Gateways() *Client {
	gateways := *c
	gateways.Endpoints = nil
	for _, e := range c.Endpoints {
		if e.Name != "irc" {
			gateways.Endpoints = append(gateways.Endpoints, e)
		}
	}
	return &gateways
}

// RoomReader reads rooms after message IDs over one gateway connection, so a
// client can follow many rooms while native IRC is unreachable without
// holding a gateway watch per room.
type RoomReader struct {
	c *Client
	k *Conn
	// readEach is set once the gateway has refused check as unknown: an
	// older gateway, read one room at a time.
	readEach bool
}

// RoomAfter is a room to read and the message ID to read after, or "" for
// its latest messages.
type RoomAfter struct {
	Room, After string
}

// Check reads rooms in one request, up to limit messages in all, one page per
// room in rooms' order. When none has news it waits up to wait for some (the
// gateway may cut the wait to its request deadline, a few seconds on most
// carriers), so a follower can check again at once. A gateway without check
// is read one room at a time, without the wait; Waits reports which.
func (r *RoomReader) Check(ctx context.Context, rooms []RoomAfter, limit int, wait time.Duration) ([]*Page, error) {
	if len(rooms) < 1 || len(rooms) > maxCheckRooms || limit < 1 || limit > 1000 || wait < 0 {
		return nil, errors.New("a check needs 1-16 rooms, a limit of 1-1000 and no negative wait")
	}
	if r.readEach {
		return r.readEachRoom(ctx, rooms, limit)
	}
	// The answer must arrive within the call's own timeout.
	wait = min(wait, maxCheckWait, r.c.callTimeout()-2*time.Second)
	req := request{RequestID: "check", Operation: "check", Limit: limit, WaitSeconds: int(max(wait, 0) / time.Second)}
	for _, room := range rooms {
		if !protocol.IsChannel(room.Room) || room.After != "" && !protocol.ValidMessageID(room.After) {
			return nil, errors.New("a check reads rooms after message IDs")
		}
		req.Rooms = append(req.Rooms, checkRoom{Name: room.Room, After: room.After})
	}
	d, err := r.c.call(ctx, r.k, req)
	var refused *gatewayError
	if errors.As(err, &refused) && (refused.failure.Code == "invalid_request" || refused.failure.Code == "unsupported_operation") {
		r.readEach = true
		return r.readEachRoom(ctx, rooms, limit)
	}
	if err != nil {
		return nil, err
	}
	if d.Check == nil || len(d.Check.Rooms) != len(rooms) {
		return nil, errors.New("gateway answered a check without a page per room")
	}
	pages := make([]*Page, len(rooms))
	for i, p := range d.Check.Rooms {
		if p.Room != rooms[i].Room {
			return nil, errors.New("gateway answered a check out of order")
		}
		for _, m := range p.Messages {
			if m == nil || !protocol.ValidMessageID(m.ID) || m.Target != p.Room {
				return nil, errors.New("gateway answered a check with a malformed message")
			}
		}
		if p.Messages == nil {
			p.Messages = []*irc.MessageMetadata{}
		}
		pages[i] = &Page{Endpoint: r.k.Endpoint, Messages: p.Messages, Status: p.Status, Cursor: p.Cursor, Gap: p.Gap}
	}
	return pages, nil
}

// Waits reports whether Check waits for news: true unless the gateway
// answered that it has no check.
func (r *RoomReader) Waits() bool { return !r.readEach }

const (
	maxCheckRooms = 16
	maxCheckWait  = 20 * time.Second
)

func (r *RoomReader) readEachRoom(ctx context.Context, rooms []RoomAfter, limit int) ([]*Page, error) {
	pages := make([]*Page, len(rooms))
	for i, room := range rooms {
		page, err := r.Read(ctx, room.Room, room.After, limit)
		if err != nil {
			return nil, err
		}
		pages[i] = page
	}
	return pages, nil
}

// ReadRooms races the client's gateway carriers (never native IRC) and reads
// over the first to connect.
func (c *Client) ReadRooms(ctx context.Context) (*RoomReader, error) {
	k, err := c.Gateways().Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &RoomReader{c: c, k: k}, nil
}

// ReadRooms reads rooms over k, a gateway connection, which the reader then
// owns.
func (k *Conn) ReadRooms() (*RoomReader, error) {
	if k.gateway == nil {
		return nil, errors.New("rooms are read this way only over a gateway carrier")
	}
	return &RoomReader{c: k.client, k: k}, nil
}

// Read returns up to limit of room's messages after a message ID (a native
// IRC cursor, or the last message seen), or its latest when after is "".
// Page.Gap reports an expired start. An error means the carrier stopped
// answering; read again on a new RoomReader.
func (r *RoomReader) Read(ctx context.Context, room, after string, limit int) (*Page, error) {
	if !protocol.IsChannel(room) || limit < 1 || limit > 1000 || after != "" && !protocol.ValidMessageID(after) {
		return nil, errors.New("a room read needs a room, a limit of 1-1000 and a message ID or nothing to start after")
	}
	start := after
	if start == "" {
		start = "recent"
	}
	page, err := r.c.gatewayRead(ctx, r.k, request{RequestID: "history", Operation: "history.read", Target: targetOf(room), Limit: limit, Start: start})
	if err != nil {
		return nil, err
	}
	for _, m := range page.Messages {
		if m == nil || !protocol.ValidMessageID(m.ID) || m.Target != room {
			return nil, errors.New("gateway answered a room read with a malformed message")
		}
	}
	return page, nil
}

// Endpoint is the carrier the reader reads over.
func (r *RoomReader) Endpoint() Endpoint { return r.k.Endpoint }

// Close closes the reader's connection.
func (r *RoomReader) Close() error { return r.k.Close() }
