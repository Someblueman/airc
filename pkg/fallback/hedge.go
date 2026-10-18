package fallback

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// hedgeRecent is how many message IDs per room, thread or inbox a hedged
// follow remembers to drop the second copy of: far more than one link can
// lag behind the other before its follow is replaced.
const hedgeRecent = 4096

// FollowHedged reads set as Follow does, but over two gateway connections
// at once, on different endpoints when the client has more than one, and
// emits each message once, from whichever connection brings it first, in
// order. A link that stalls silently (a dead NAT path, a frozen network)
// or drops then costs nothing while the other carries on; it is replaced
// in the background, starting after what was already emitted. Unlike
// Follow it renews leases itself and emits only frames with news. It
// returns when ctx ends (nil), when emit returns an error (that error), or
// with the last failure (ErrNoFollow when the gateway does not follow)
// once both connections' follows have failed without carrying anything.
// Each follow holds its own gateway session, so a hedged follow costs the
// gateway two.
func (c *Client) FollowHedged(ctx context.Context, set CheckSet, idle time.Duration, emit func(Checked) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	gateways := c.Gateways()
	gateways.Hedge = false // each leg follows on one connection
	if attempted := c.Attempted; attempted != nil {
		var mu sync.Mutex // the legs connect at once; Attempted is never called concurrently
		gateways.Attempted = func(e Endpoint, err error) {
			mu.Lock()
			defer mu.Unlock()
			attempted(e, err)
		}
	}
	h := &hedge{c: gateways, set: set, idle: idle, emit: emit, done: make(chan error, 1), cancel: cancel}
	h.init()
	var legs sync.WaitGroup
	for i := range 2 {
		legs.Add(1)
		go func() {
			defer legs.Done()
			h.leg(ctx, i)
		}()
	}
	var err error
	select {
	case err = <-h.done:
	case <-ctx.Done():
	}
	cancel()
	legs.Wait()
	return err
}

type hedge struct {
	c      *Client
	set    CheckSet
	idle   time.Duration
	emit   func(Checked) error
	done   chan error
	cancel context.CancelFunc

	mu sync.Mutex
	// emitting serializes emit, and with mu the positions it advances.
	emitting sync.Mutex
	// rooms, threads and inbox are the positions emitted so far and the
	// IDs recently emitted, in set's order.
	rooms, threads []*hedgePosition
	inbox          *hedgePosition
	// endpoints are the legs' current endpoints, and failing whether each
	// leg's last follow ended in failure before carrying anything.
	endpoints [2]Endpoint
	failing   [2]bool
}

type hedgePosition struct {
	after string
	seen  map[string]bool
	order []string
	// gapTold is set once a gap at the start has been emitted.
	gapTold bool
}

func (h *hedge) init() {
	for _, r := range h.set.Rooms {
		h.rooms = append(h.rooms, newHedgePosition(r.After))
	}
	for _, t := range h.set.Threads {
		h.threads = append(h.threads, newHedgePosition(t.After))
	}
	if h.set.Inbox != nil {
		h.inbox = newHedgePosition(*h.set.Inbox)
	}
}

func newHedgePosition(after string) *hedgePosition {
	return &hedgePosition{after: after, seen: map[string]bool{}}
}

// remember records id as emitted, forgetting the oldest beyond hedgeRecent.
func (p *hedgePosition) remember(id string) {
	p.seen[id] = true
	p.order = append(p.order, id)
	if len(p.order) > hedgeRecent {
		delete(p.seen, p.order[0])
		p.order = p.order[1:]
	}
}

// current is set with each position advanced to what has been emitted.
func (h *hedge) current() CheckSet {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.set
	set.Rooms = make([]RoomAfter, len(h.set.Rooms))
	for i, r := range h.set.Rooms {
		set.Rooms[i] = RoomAfter{Room: r.Room, After: h.rooms[i].after}
	}
	set.Threads = make([]ThreadAfter, len(h.set.Threads))
	for i, t := range h.set.Threads {
		set.Threads[i] = ThreadAfter{ID: t.ID, After: h.threads[i].after}
	}
	if h.inbox != nil {
		after := h.inbox.after
		set.Inbox = &after
	}
	return set
}

// finish ends the hedged follow with err, once.
func (h *hedge) finish(err error) {
	select {
	case h.done <- err:
	default:
	}
	h.cancel()
}

// leg keeps one of the two follows running until ctx ends: it connects,
// preferring an endpoint the other leg is not on, follows from the
// positions emitted so far, and starts again when the lease ends or the
// link fails, after a backoff for a failure. When both legs' last follows
// failed without carrying anything, the hedged follow ends with the
// failure.
func (h *hedge) leg(ctx context.Context, i int) {
	backoff := time.Second
	for ctx.Err() == nil {
		carried, err := h.follow(ctx, i)
		var stopped hedgeEmitError
		switch {
		case ctx.Err() != nil:
			return
		case errors.As(err, &stopped):
			h.finish(stopped.err)
			return
		case err == nil:
			backoff = time.Second
			continue // the lease ended: renew from the positions reached
		}
		h.mu.Lock()
		h.failing[i] = !carried
		both := h.failing[0] && h.failing[1]
		h.mu.Unlock()
		if both {
			h.finish(err)
			return
		}
		if carried {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// hedgeEmitError carries the error emit returned, which ends the follow.
type hedgeEmitError struct{ err error }

func (e hedgeEmitError) Error() string { return e.err.Error() }

// follow runs one follow for leg i, reporting whether it carried any frame.
func (h *hedge) follow(ctx context.Context, i int) (carried bool, err error) {
	k, err := h.connect(ctx, i)
	if err != nil {
		return false, err
	}
	reader := &RoomReader{c: h.c, k: k}
	defer reader.Close()
	h.mu.Lock()
	h.endpoints[i] = k.Endpoint
	h.mu.Unlock()
	set := h.current()
	err = reader.Follow(ctx, set, h.idle, func(checked Checked) error {
		if !carried {
			carried = true
			h.mu.Lock()
			h.failing[i] = false
			h.mu.Unlock()
		}
		if err := h.merge(set, checked); err != nil {
			return hedgeEmitError{err}
		}
		return nil
	})
	return carried, err
}

// connect opens a gateway connection for leg i, trying the endpoints the
// other leg is not on first. The legs start from different ends of the
// preference order, since both connect at once before either knows the
// other's endpoint.
func (h *hedge) connect(ctx context.Context, i int) (*Conn, error) {
	h.mu.Lock()
	avoid := h.endpoints[1-i]
	h.mu.Unlock()
	order := slices.Clone(h.c.Endpoints)
	if i == 1 && len(order) > 1 {
		order = append(order[1:], order[0])
	}
	c := *h.c
	c.Endpoints = nil
	var last []Endpoint
	for _, e := range order {
		if e == avoid {
			last = append(last, e)
		} else {
			c.Endpoints = append(c.Endpoints, e)
		}
	}
	c.Endpoints = append(c.Endpoints, last...)
	return c.Connect(ctx)
}

// merge emits what checked, an answer to set, holds that has not been
// emitted: each page keeps only messages not seen yet, in order, and
// advances its position.
func (h *hedge) merge(set CheckSet, checked Checked) error {
	h.emitting.Lock()
	defer h.emitting.Unlock()
	h.mu.Lock()
	out := Checked{}
	news := false
	keep := func(p *hedgePosition, page *Page) *Page {
		if page == nil {
			return nil
		}
		kept := *page
		kept.Messages = nil
		for _, m := range page.Messages {
			if !p.seen[m.ID] {
				p.remember(m.ID)
				kept.Messages = append(kept.Messages, m)
			}
		}
		switch {
		case len(kept.Messages) > 0:
			p.after = kept.Messages[len(kept.Messages)-1].ID
		case page.Gap && len(page.Messages) == 0 && len(p.order) == 0:
			p.after = "" // nothing retained after the expired start
		case len(page.Messages) == 0 && page.Status == "ok" && page.Cursor != "":
			p.after = page.Cursor // an idle page's watermark
		}
		// A gap is told once: by the first link to reach it.
		kept.Gap = page.Gap && !p.gapTold
		p.gapTold = p.gapTold || page.Gap
		news = news || len(kept.Messages) > 0 || kept.Gap
		return &kept
	}
	for j, page := range checked.Rooms {
		if j < len(set.Rooms) {
			out.Rooms = append(out.Rooms, keep(h.rooms[j], page))
		}
	}
	for j, page := range checked.Threads {
		if j < len(set.Threads) {
			out.Threads = append(out.Threads, keep(h.threads[j], page))
		}
	}
	if h.inbox != nil {
		out.Inbox = keep(h.inbox, checked.Inbox)
	}
	h.mu.Unlock()
	if !news && !hasMore(out) {
		return nil
	}
	return h.emit(out)
}

func hasMore(checked Checked) bool {
	pages := append(append([]*Page(nil), checked.Rooms...), checked.Threads...)
	if checked.Inbox != nil {
		pages = append(pages, checked.Inbox)
	}
	for _, page := range pages {
		if page != nil && page.Status == "more" {
			return true
		}
	}
	return false
}
