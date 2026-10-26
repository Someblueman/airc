package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/mqttwire"
)

// Shared subscriptions ($share/<group>/<topic>) spread a room's, a
// thread's or the inbox's messages over the sessions that subscribe to it
// in the same group: each message goes to one of them, in turn, as a pool
// of workers wants. A group belongs to one author, so another author's
// group of the same name is a different group, and its position is kept
// while any session, connected or not, still subscribes to it. One
// followed check per group, run as whichever member is connected, carries
// it; a message handed to a member stays in that member's session until
// acknowledged.

// mqtt5Group is one author's shared subscription to one topic.
type mqtt5Group struct {
	owner, name string
	kind        mqtt5Kind
	target      string
	// after is the group's position: the next message comes after it.
	after string
	// members are the connected sessions subscribed, in the order they
	// joined; next is where the round robin goes next.
	members []*mqtt5Conn
	next    int
	cancel  context.CancelFunc
	done    chan struct{}
}

func (g *mqtt5Group) topic() string { return mqtt5Topic(g.kind, g.target) }

func mqtt5GroupKey(owner, name string, kind mqtt5Kind, target string) string {
	return owner + "\x00" + name + "\x00" + mqtt5Topic(kind, target)
}

// parseShared splits a $share/<group>/<topic> filter. ok is false for any
// other filter; a shared filter with no valid group gives group "".
func parseShared(filter string) (group, topic string, ok bool) {
	rest, ok := strings.CutPrefix(filter, "$share/")
	if !ok {
		return "", "", false
	}
	group, topic, found := strings.Cut(rest, "/")
	if !found || group == "" || strings.ContainsAny(group, "+#") || topic == "" {
		return "", "", true
	}
	return group, topic, true
}

// syncGroups makes c a member of exactly the groups its session's shared
// subscriptions name, starting their follows, and forgets groups no
// session subscribes to any more.
func (s *mqtt5Store) syncGroups(c *mqtt5Conn) {
	c.state.mu.Lock()
	want := map[string]*mqtt5Sub{}
	owner := c.state.owner
	for _, sub := range c.state.subs {
		if sub.group != "" {
			want[mqtt5GroupKey(owner, sub.group, sub.kind, sub.target)] = sub
		}
	}
	c.state.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, g := range s.groups {
		if i := memberIndex(g, c); i >= 0 && want[key] == nil {
			s.leaveLocked(key, g, i)
		}
	}
	if s.closed {
		return
	}
	for key, sub := range want {
		g := s.groups[key]
		if g == nil {
			g = &mqtt5Group{owner: owner, name: sub.group, kind: sub.kind, target: sub.target, after: sub.after}
			s.groups[key] = g
		}
		if memberIndex(g, c) < 0 {
			g.members = append(g.members, c)
		}
		if g.cancel == nil {
			s.startGroupLocked(g)
		}
	}
}

// leaveGroups ends c's membership of every group, when its connection
// ends; its session's subscriptions stay.
func (s *mqtt5Store) leaveGroups(c *mqtt5Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, g := range s.groups {
		if i := memberIndex(g, c); i >= 0 {
			s.leaveLocked(key, g, i)
		}
	}
}

func memberIndex(g *mqtt5Group, c *mqtt5Conn) int {
	for i, m := range g.members {
		if m == c {
			return i
		}
	}
	return -1
}

// leaveLocked removes member i; a group left without members stops its
// follow, and one no session subscribes to is forgotten.
func (s *mqtt5Store) leaveLocked(key string, g *mqtt5Group, i int) {
	g.members = append(g.members[:i], g.members[i+1:]...)
	if g.next > i {
		g.next--
	}
	if len(g.members) > 0 {
		return
	}
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	if !s.subscribedLocked(key) {
		delete(s.groups, key)
	}
}

// subscribedLocked reports whether any session subscribes to group key.
func (s *mqtt5Store) subscribedLocked(key string) bool {
	for _, state := range s.sessions {
		state.mu.Lock()
		for _, sub := range state.subs {
			if sub.group != "" && mqtt5GroupKey(state.owner, sub.group, sub.kind, sub.target) == key {
				state.mu.Unlock()
				return true
			}
		}
		state.mu.Unlock()
	}
	return false
}

// startGroupLocked starts g's follow, which runs while it has members.
func (s *mqtt5Store) startGroupLocked(g *mqtt5Group) {
	ctx, cancel := context.WithCancel(s.ctx)
	previous, done := g.done, make(chan struct{})
	g.cancel, g.done = cancel, done
	s.groupsRunning.Add(1)
	go func() {
		defer s.groupsRunning.Done()
		defer close(done)
		if previous != nil {
			<-previous // one follow per group, never two at once
		}
		s.runGroup(ctx, g)
	}()
}

// pruneGroupsLocked forgets groups with no members that no session
// subscribes to any more.
func (s *mqtt5Store) pruneGroupsLocked() {
	for key, g := range s.groups {
		if len(g.members) == 0 && !s.subscribedLocked(key) {
			delete(s.groups, key)
		}
	}
}

// driver is a connected member to run the group's follow as, or nil.
func (s *mqtt5Store) driver(g *mqtt5Group) *mqtt5Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range g.members {
		if m.ctx.Err() == nil {
			return m
		}
	}
	return nil
}

// runGroup keeps one followed check over g's topic running, as one of its
// members, until ctx ends: when that member's connection ends, another
// member's takes over from the group's position.
func (s *mqtt5Store) runGroup(ctx context.Context, g *mqtt5Group) {
	backoff := time.Second
	for ctx.Err() == nil {
		m := s.driver(g)
		if m == nil {
			select { // members are leaving; the last one cancels ctx
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
		s.mu.Lock()
		after := g.after
		s.mu.Unlock()
		if after == "" {
			after = "recent"
		}
		r := core.Request{Version: 1, RequestID: "mqtt-shared", Operation: core.Check, Limit: mqtt5FollowLimit}
		switch g.kind {
		case mqtt5Room:
			r.Rooms = []core.CheckRoom{{Name: g.target, After: after}}
		case mqtt5Thread:
			r.Threads = []core.CheckThread{{ID: g.target, After: after}}
		case mqtt5Inbox:
			r.Inbox = &core.CheckInbox{After: after}
		}
		followCtx, stop := context.WithCancel(ctx)
		unlink := context.AfterFunc(m.ctx, stop)
		err := m.d.SubmitDecodedStream(followCtx, m.in, core.Submission{Request: r}, time.Now(), dispatch.StreamSink{Check: func(result core.CheckResult) error {
			return s.deliverGroup(followCtx, g, result)
		}})
		unlink()
		stop()
		switch {
		case ctx.Err() != nil:
			return
		case err == nil, errors.Is(err, backend.ErrStreamRenew), m.ctx.Err() != nil:
			backoff = time.Second
			continue // a new lease, or another member's turn to drive
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// deliverGroup hands each message of one follow frame to one member, in
// turn, and moves the group's position past it.
func (s *mqtt5Store) deliverGroup(ctx context.Context, g *mqtt5Group, result core.CheckResult) error {
	var page *core.CheckPage
	switch {
	case len(result.Rooms) == 1:
		page = &result.Rooms[0]
	case len(result.Threads) == 1:
		page = &result.Threads[0]
	case result.Inbox != nil:
		page = result.Inbox
	default:
		return errors.New("shared follow frame without its page")
	}
	if page.Status == "withheld" || page.Status == "expired" {
		return nil
	}
	format := byte(1)
	for j, m := range page.Messages {
		body, err := json.Marshal(m)
		if err != nil {
			return err
		}
		props := mqttwire.Properties{PayloadFormat: &format, ContentType: "application/json"}
		if j == 0 && page.Gap {
			props.User = [][2]string{{"airc-gap", "true"}}
		}
		if err := s.handOut(ctx, g, props, body); err != nil {
			return err
		}
		s.mu.Lock()
		g.after = m.ID
		s.mu.Unlock()
	}
	if len(page.Messages) == 0 && page.Cursor != "" {
		s.mu.Lock()
		g.after = page.Cursor
		s.mu.Unlock()
	}
	return nil
}

// handOut publishes one message to the next member with room under its
// Receive Maximum, or, when none has room, waits on the next in turn as a
// lone subscription would. A member whose connection fails is skipped.
func (s *mqtt5Store) handOut(ctx context.Context, g *mqtt5Group, props mqttwire.Properties, body []byte) error {
	for {
		s.mu.Lock()
		n := len(g.members)
		if n == 0 {
			s.mu.Unlock()
			return errors.New("no member to hand a shared message to")
		}
		chosen := -1
		for i := range n {
			m := g.members[(g.next+i)%n]
			if m.ctx.Err() == nil && (m.groupQoS(g) == 0 || len(m.slots) < cap(m.slots)) {
				chosen = (g.next + i) % n
				break
			}
		}
		if chosen < 0 {
			chosen = g.next % n // all full: queue on the next in turn
		}
		member := g.members[chosen]
		qos := member.groupQoS(g)
		g.next = (chosen + 1) % n
		s.mu.Unlock()
		memberCtx, stop := context.WithCancel(ctx)
		unlink := context.AfterFunc(member.ctx, stop)
		err := member.send(memberCtx, g.topic(), qos, props, body, true)
		unlink()
		stop()
		if err == nil {
			return nil
		}
		// That member's connection ended: the message goes to another,
		// once the member has left.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// groupQoS is the QoS of c's subscription to g.
func (c *mqtt5Conn) groupQoS(g *mqtt5Group) byte {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	for _, sub := range c.state.subs {
		if sub.group == g.name && sub.kind == g.kind && sub.target == g.target {
			return sub.qos
		}
	}
	return 0
}
