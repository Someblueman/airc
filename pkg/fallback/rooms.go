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
		if !NativeCarrier(e.Name) {
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
	// older gateway, read one room at a time. noThreads and noInbox are set
	// once it has refused those parts of a check, noFollow once it has
	// refused a followed check.
	readEach, noThreads, noInbox, noFollow bool
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
	if len(rooms) < 1 {
		return nil, errors.New("a check needs a room")
	}
	checked, err := r.CheckAll(ctx, CheckSet{Rooms: rooms, Limit: limit, Wait: wait})
	return checked.Rooms, err
}

// CheckInbox is Check that also reads the author's inbox after inboxAfter;
// see CheckSet.
func (r *RoomReader) CheckInbox(ctx context.Context, rooms []RoomAfter, inboxAfter string, limit int, wait time.Duration) ([]*Page, *Page, error) {
	checked, err := r.CheckAll(ctx, CheckSet{Rooms: rooms, Inbox: &inboxAfter, Limit: limit, Wait: wait})
	return checked.Rooms, checked.Inbox, err
}

// ThreadAfter is a followed thread (its root's ID) and the message ID to
// read it after, "" for its latest messages or "retained" for its oldest.
type ThreadAfter struct {
	ID, After string
}

// CheckSet is what one check reads: rooms, followed threads, and the
// author's inbox from Inbox ("" for its latest messages, "retained" for its
// oldest, or a message ID; nil leaves it out). They number at most 16 in
// all. Limit bounds the messages in all, and Wait is as for Check.
type CheckSet struct {
	Rooms   []RoomAfter
	Threads []ThreadAfter
	Inbox   *string
	Limit   int
	Wait    time.Duration
}

// Checked is a check's pages, in CheckSet's order. Threads and Inbox are nil
// when the gateway serves none (one without check, or from before them).
//
// A thread page's Status may be "withheld" (it has news the gateway does not
// show this author, such as direct messages) or "expired" (its root is no
// longer retained). The inbox holds mentions in rooms the gateway lets the
// author read and, for a registered author it can prove them for, direct
// messages; MentionsOnly marks one without them, which must not advance a
// position that also covers direct messages.
type Checked struct {
	Rooms, Threads []*Page
	Inbox          *Page
}

// CheckAll reads set in one check.
func (r *RoomReader) CheckAll(ctx context.Context, set CheckSet) (Checked, error) {
	if set.Inbox != nil && *set.Inbox != "" && *set.Inbox != "retained" && !protocol.ValidMessageID(*set.Inbox) {
		return Checked{}, errors.New("an inbox is read after a message ID")
	}
	for {
		if r.noThreads {
			set.Threads = nil
		}
		if r.noInbox {
			set.Inbox = nil
		}
		if len(set.Rooms) == 0 && len(set.Threads) == 0 && set.Inbox == nil {
			return Checked{}, nil
		}
		checked, err := r.check(ctx, set)
		var refused *gatewayError
		if !errors.As(err, &refused) {
			return checked, err
		}
		// A gateway from before threads or the inbox refuses them as an
		// unknown request; a gateway may refuse a guest's inbox. Leave them
		// out from then on rather than lose the rooms.
		switch code := refused.failure.Code; {
		case len(set.Threads) > 0 && (code == "invalid_request" || code == "unsupported_operation"):
			r.noThreads = true
		case set.Inbox != nil && (code == "invalid_request" || code == "unsupported_operation" || code == "forbidden"):
			r.noInbox = true
		case code == "invalid_request" || code == "unsupported_operation":
			r.readEach = true
		default:
			return Checked{}, err
		}
	}
}

// Waits reports whether Check waits for news: true unless the gateway
// answered that it has no check.
func (r *RoomReader) Waits() bool { return !r.readEach }

// ServesInbox reports whether CheckAll may read an inbox: false once the
// gateway has refused it or has no check.
func (r *RoomReader) ServesInbox() bool { return !r.noInbox && !r.readEach }

// ServesThreads reports whether CheckAll may read threads.
func (r *RoomReader) ServesThreads() bool { return !r.noThreads && !r.readEach }

func (r *RoomReader) check(ctx context.Context, set CheckSet) (Checked, error) {
	if r.readEach {
		if err := validCheckSet(set); err != nil {
			return Checked{}, err
		}
		pages, err := r.readEachRoom(ctx, set.Rooms, set.Limit)
		return Checked{Rooms: pages}, err
	}
	req, err := r.checkRequest(set)
	if err != nil {
		return Checked{}, err
	}
	d, err := r.c.call(ctx, r.k, req)
	if err != nil {
		return Checked{}, err
	}
	return r.checked(d.Check, set)
}

func validCheckSet(set CheckSet) error {
	targets := len(set.Rooms) + len(set.Threads)
	if set.Inbox != nil {
		targets++
	}
	if targets < 1 || targets > maxCheckRooms || set.Limit < 1 || set.Limit > 1000 || set.Wait < 0 {
		return errors.New("a check needs 1-16 rooms, threads and inbox, a limit of 1-1000 and no negative wait")
	}
	return nil
}

// checkRequest is set as a contract check request.
func (r *RoomReader) checkRequest(set CheckSet) (request, error) {
	if err := validCheckSet(set); err != nil {
		return request{}, err
	}
	// The answer must arrive within the call's own timeout.
	wait := min(set.Wait, maxCheckWait, r.c.callTimeout()-2*time.Second)
	req := request{RequestID: "check", Operation: "check", Limit: set.Limit, WaitSeconds: int(max(wait, 0) / time.Second)}
	for _, room := range set.Rooms {
		if !protocol.IsChannel(room.Room) || room.After != "" && !protocol.ValidMessageID(room.After) {
			return request{}, errors.New("a check reads rooms after message IDs")
		}
		req.Rooms = append(req.Rooms, checkRoom{Name: room.Room, After: room.After})
	}
	for _, thread := range set.Threads {
		if !protocol.ValidMessageID(thread.ID) || thread.After != "" && thread.After != "retained" && !protocol.ValidMessageID(thread.After) {
			return request{}, errors.New("a check reads threads by root ID after message IDs")
		}
		req.Threads = append(req.Threads, checkThread(thread))
	}
	if set.Inbox != nil {
		req.Inbox = &checkInbox{After: *set.Inbox}
	}
	return req, nil
}

// ErrNoFollow is a gateway, or a carrier, without followed checks: poll with
// CheckAll instead.
var ErrNoFollow = errors.New("the gateway does not follow checks on this carrier")

// Follows reports whether Follow may work: the carrier streams and the
// gateway has not refused a follow.
func (r *RoomReader) Follows() bool {
	_, ok := r.k.gateway.(streamConn)
	return ok && !r.readEach && !r.noFollow
}

// Follow reads set as one stream: the snapshot first, then a Checked for
// every batch of news and an empty one every keepalive, each page carrying
// the position to continue from, until the gateway's follow lease ends
// (nil: resume from the last positions with another Follow), emit returns
// an error (returned), ctx ends, or nothing at all arrives for idle (the
// link is presumed dead). The follow owns the connection: nothing else may
// use the reader until it returns, and after an error the reader is closed
// and must be reopened. ErrNoFollow means poll with CheckAll.
func (r *RoomReader) Follow(ctx context.Context, set CheckSet, idle time.Duration, emit func(Checked) error) error {
	stream, ok := r.k.gateway.(streamConn)
	if !ok || r.readEach || r.noFollow {
		return ErrNoFollow
	}
	if r.c.Hedge {
		return r.c.FollowHedged(ctx, set, idle, emit)
	}
	if r.noThreads {
		set.Threads = nil
	}
	if r.noInbox {
		set.Inbox = nil
	}
	set.Wait = 0
	req, err := r.checkRequest(set)
	if err != nil {
		return err
	}
	if idle <= 0 {
		idle = time.Minute
	}
	req.Version, req.Author, req.Password = 1, r.c.author(), r.c.Password
	err = stream.follow(ctx, req, idle, func(result *checkResult) error {
		checked, err := r.checked(result, set)
		if err != nil {
			return err
		}
		return emit(checked)
	})
	var refused *gatewayError
	if errors.As(err, &refused) && (refused.failure.Code == "invalid_request" || refused.failure.Code == "unsupported_operation") {
		// A gateway from before followed checks refuses the stream.
		r.noFollow = true
		return ErrNoFollow
	}
	if err != nil {
		_ = r.Close()
	}
	return err
}

// checked validates a check's answer against set.
func (r *RoomReader) checked(d *checkResult, set CheckSet) (Checked, error) {
	if d == nil || len(d.Rooms) != len(set.Rooms) || len(d.Threads) != len(set.Threads) || (set.Inbox != nil) != (d.Inbox != nil) {
		return Checked{}, errors.New("gateway answered a check without a page per room")
	}
	page := func(p checkPage, room string) (*Page, error) {
		for _, m := range p.Messages {
			if m == nil || !protocol.ValidMessageID(m.ID) || room != "" && m.Target != room {
				return nil, errors.New("gateway answered a check with a malformed message")
			}
		}
		if p.Messages == nil {
			p.Messages = []*irc.MessageMetadata{}
		}
		return &Page{Endpoint: r.k.Endpoint, Messages: p.Messages, Status: p.Status, Cursor: p.Cursor, Gap: p.Gap, MentionsOnly: p.MentionsOnly}, nil
	}
	var checked Checked
	for i, p := range d.Rooms {
		if p.Room != set.Rooms[i].Room {
			return Checked{}, errors.New("gateway answered a check out of order")
		}
		next, err := page(p, p.Room)
		if err != nil {
			return Checked{}, err
		}
		checked.Rooms = append(checked.Rooms, next)
	}
	for i, p := range d.Threads {
		if p.Room != "thread:"+set.Threads[i].ID {
			return Checked{}, errors.New("gateway answered a check out of order")
		}
		next, err := page(p, "")
		if err != nil {
			return Checked{}, err
		}
		checked.Threads = append(checked.Threads, next)
	}
	if d.Inbox != nil {
		var err error
		if checked.Inbox, err = page(*d.Inbox, ""); err != nil {
			return Checked{}, err
		}
	}
	return checked, nil
}

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
