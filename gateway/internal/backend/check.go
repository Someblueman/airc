package backend

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	// checkWaitMargin is kept between a check's wait and its request's
	// deadline, for the second snapshot and the answer.
	checkWaitMargin = 750 * time.Millisecond
	// minCheckWait is the shortest wait worth observing for.
	minCheckWait = 500 * time.Millisecond
	// checkKeepAlive keeps a waiting session reading well inside its 10 s
	// read timeout (see dialTemplate).
	checkKeepAlive = 4 * time.Second
)

// Check reads every room of a check grant with one native CHECK on one
// session. Asked to wait, it first observes the rooms (as AIRC's own
// waiting clients do, so news around the snapshot is either in it or wakes
// the wait); when the snapshot has nothing new, it gives up its session slot
// for a wait slot and answers after the first live message in any room, or
// with the empty snapshot when the wait ends. The wait is cut to the
// request's own deadline, and skipped when limits.waits is used up.
//
// The inbox page is AIRC's "@nick" selector as the author, and a thread
// page its "thread:ID" selector, limited by scope.
func (b *Backend) Check(ctx context.Context, grant *store.ReadGrant, scope CheckScope) (core.CheckResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.CheckResult{}, err
	}
	r := a.Request
	if r.Operation != core.Check || len(r.Rooms) == 0 && len(r.Threads) == 0 && r.Inbox == nil {
		return core.CheckResult{}, store.ErrInvalid
	}
	scope.DirectMessages = scope.DirectMessages && a.Author.Registered
	var result core.CheckResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		if client.Features()["CHECK"] != "1" {
			return ErrReadUnsupported
		}
		p := checkPlan{request: r, scope: scope, nick: client.Nick()}
		if !strings.EqualFold(p.nick, a.Author.Nick) {
			return auth.ErrForbidden
		}
		if scope.DirectMessages {
			// Direct messages are proven against the account this session
			// logged in as, never the nickname alone.
			p.account = client.AccountID()
			if !validHistoryID(p.account) {
				return auth.ErrForbidden
			}
		}
		wait := time.Duration(r.WaitSeconds) * time.Second
		if deadline, ok := overall.Deadline(); ok {
			wait = min(wait, time.Until(deadline)-checkWaitMargin)
		}
		lane := a.Lane()
		waiting := wait >= minCheckWait && b.acquireWait(lane)
		if waiting {
			defer b.releaseWait(lane)
			// Leave nothing observed on a session that may be kept. Should
			// UNOBSERVE fail, closing the session keeps it from reuse.
			defer func() {
				if unobserve(client, p.selectors()) != nil {
					_ = client.Close()
				}
			}()
			all, err := observe(overall, client, p.selectors())
			if err != nil {
				return err
			}
			// AIRC skips a room it would not show this author; the snapshot
			// then refuses it.
			waiting = all
		}
		var woke bool
		news := func(event irc.Event) error {
			woke = woke || p.wakes(event)
			return nil
		}
		result, err = p.snapshot(overall, client, news)
		if err != nil || !waiting || woke || !emptyCheck(result) {
			return err
		}
		// Nothing new: wait without holding a session slot.
		b.release(lane, store.LookupAttempt)
		b.countAwaiting(1)
		woke, err = p.await(overall, client, wait)
		b.countAwaiting(-1)
		if !b.reacquire(lane) || err != nil || !woke {
			return err
		}
		result, err = p.snapshot(overall, client, nil)
		return err
	})
	if err != nil {
		return core.CheckResult{}, err
	}
	return result, nil
}

// Awaiting is how many checks are waiting for news after an empty
// snapshot, so tests can post only once a check waits: AIRC answered its
// snapshot before the post, so the post can reach it only by waking it.
func (b *Backend) Awaiting() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.awaiting
}

func (b *Backend) countAwaiting(delta int) {
	b.mu.Lock()
	b.awaiting += delta
	b.mu.Unlock()
}

// CheckScope is what a check may show beyond its named rooms: the rooms
// (Readable) whose messages may appear as mentions and in threads, and
// whether the inbox may show direct messages, which AIRC proves only for a
// registered author's account.
type CheckScope struct {
	Readable       func(room string) bool
	DirectMessages bool
}

func (s CheckScope) readable(room string) bool { return s.Readable != nil && s.Readable(room) }

// checkPlan is one check's native selectors: its rooms, its threads
// ("thread:ID"), then the author's inbox ("@nick") when asked for, with the
// account that proves a direct message was the author's.
type checkPlan struct {
	request       core.Request
	scope         CheckScope
	nick, account string
}

func (p checkPlan) selectors() []string {
	names := make([]string, 0, len(p.request.Rooms)+len(p.request.Threads)+1)
	for _, room := range p.request.Rooms {
		names = append(names, room.Name)
	}
	for _, thread := range p.request.Threads {
		names = append(names, "thread:"+thread.ID)
	}
	if p.request.Inbox != nil {
		names = append(names, "@"+p.nick)
	}
	return names
}

// positions is each selector's start, in selectors' order.
func (p checkPlan) positions() []string {
	after := make([]string, 0, len(p.request.Rooms)+len(p.request.Threads)+1)
	for _, room := range p.request.Rooms {
		after = append(after, room.After)
	}
	for _, thread := range p.request.Threads {
		after = append(after, thread.After)
	}
	if p.request.Inbox != nil {
		after = append(after, p.request.Inbox.After)
	}
	return after
}

// wakes reports a live message for the check: in one of its rooms or, when
// it reads threads or the inbox, any message the session observes, as AIRC
// delivers only what the session observes.
func (p checkPlan) wakes(event irc.Event) bool {
	m, ok := event.(*irc.MessageEvent)
	if !ok || m.Type != "message" {
		return false
	}
	if p.request.Inbox != nil || len(p.request.Threads) > 0 {
		return true
	}
	for _, room := range p.request.Rooms {
		if room.Name == m.Target {
			return true
		}
	}
	return false
}

// inboxMessage reports an inbox message the author may read: a mention in
// a room it may read, or a direct message AIRC recorded as received by its
// account. The selector also matches direct messages to the nickname from
// before the account held it; nothing proves those were the author's.
func (p checkPlan) inboxMessage(m *irc.MessageMetadata) bool {
	if protocol.IsChannel(m.Target) {
		return p.scope.readable(m.Target)
	}
	return p.scope.DirectMessages && p.account != "" && strings.EqualFold(m.Target, p.nick) && m.RecipientAccountID == p.account
}

// observe observes targets and reports whether AIRC confirmed every one.
// AIRC skips a room the session may not read without replying, so a ping
// marks the end of the confirmations.
func observe(ctx context.Context, client *irc.Client, targets []string) (bool, error) {
	if err := client.Observe(targets...); err != nil {
		return false, ErrReadUnavailable
	}
	token := "airc-gw-observe-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := client.Raw("PING :" + token); err != nil {
		return false, ErrReadUnavailable
	}
	confirmed := map[string]bool{}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return false, ErrReadUnavailable
			}
			raw, ok := event.(*irc.RawEvent)
			if !ok {
				continue
			}
			switch {
			case raw.Command == "765" && len(raw.Params) == 2:
				confirmed[raw.Params[1]] = true
			case raw.Command == "430" && len(raw.Params) == 2 && strings.HasPrefix(raw.Params[1], "thread:"):
				// A thread AIRC no longer retains: nothing live can come
				// for it, and the snapshot reports it expired.
				confirmed[raw.Params[1]] = true
			case raw.Command == "PONG" && raw.Trailing == token:
				return len(confirmed) == len(targets), nil
			case rejectedRead(raw):
				return false, ErrReadUnavailable
			}
		case <-ctx.Done():
			return false, ErrReadUnavailable
		}
	}
}

func unobserve(client *irc.Client, targets []string) error {
	return client.Raw("UNOBSERVE " + strings.Join(targets, ","))
}

// await reports whether a live message for the check arrived before wait
// ended. It pings every checkKeepAlive, as a quiet wait may outlast the
// session's read timeout; the pongs are discarded here, or by the session's
// next reuse.
func (p checkPlan) await(ctx context.Context, client *irc.Client, wait time.Duration) (bool, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	keepAlive := time.NewTicker(checkKeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-keepAlive.C:
			if err := client.Raw("PING :airc-gw-keepalive"); err != nil {
				return false, ErrReadUnavailable
			}
		case event, ok := <-client.Events():
			if !ok {
				return false, ErrReadUnavailable
			}
			if raw, ok := event.(*irc.RawEvent); ok && rejectedRead(raw) {
				return false, ErrReadUnavailable
			}
			if p.wakes(event) {
				return true, nil
			}
		case <-timer.C:
			return false, nil
		case <-ctx.Done():
			return false, ErrReadUnavailable
		}
	}
}

// snapshot runs one CHECK for every selector and validates the pages. An
// answer too large to carry is asked for again with half as many messages,
// down to one, so a backlog of large messages reads as pages with more to
// come rather than a refusal that every retry from the same positions
// would meet; AIRC cuts its pages at the messages it leaves out.
func (p checkPlan) snapshot(ctx context.Context, client *irc.Client, other func(irc.Event) error) (core.CheckResult, error) {
	r := p.request
	after := p.positions()
	var entries []irc.CheckEntry
	for limit := r.Limit; ; limit = max(limit/2, 1) {
		request := irc.CheckRequest{IncludeOwn: true, MaxMessages: limit}
		for i, selector := range p.selectors() {
			target := irc.CheckTarget{Target: selector, Limit: limit}
			switch after[i] {
			case "recent":
			case "retained":
				target.After = "*"
			default:
				target.After = after[i]
			}
			request.Targets = append(request.Targets, target)
		}
		var err error
		entries, err = boundedCheck(ctx, client, request, limit+len(request.Targets), other)
		if errors.Is(err, store.ErrCapacity) && limit > 1 {
			continue
		}
		if err != nil {
			return core.CheckResult{}, err
		}
		break
	}
	if ctx.Err() != nil {
		return core.CheckResult{}, ErrReadUnavailable
	}
	return p.pages(entries, after)
}

// pages turns CHECK's entries (every message once, oldest first, naming the
// selectors whose pages hold it, then one page marker per selector in
// request order) into a page per room and thread and the inbox page. The
// inbox keeps only what the author may read; a thread with news outside
// the rooms it may read is withheld whole, and one whose root AIRC no
// longer retains is expired.
func (p checkPlan) pages(entries []irc.CheckEntry, after []string) (core.CheckResult, error) {
	r := p.request
	selectors := p.selectors()
	pages := make([]core.CheckPage, len(selectors))
	last := make([]string, len(selectors)) // each page's last message, kept or not
	withheld := make([]bool, len(selectors))
	for i, selector := range selectors {
		pages[i] = core.CheckPage{Room: selector, Messages: []*irc.MessageMetadata{}}
	}
	threads := len(r.Rooms) // first thread page
	inbox := -1
	if r.Inbox != nil {
		inbox = len(selectors) - 1
		pages[inbox].MentionsOnly = !p.scope.DirectMessages
	}
	thread := func(i int) bool { return i >= threads && i < threads+len(r.Threads) }
	seen := map[string]bool{}
	// order is every message, oldest first; routed[i] the messages AIRC
	// put on page i, shown or not, so a page can be cut to fit.
	var order []*irc.MessageMetadata
	routed := make([][]string, len(selectors))
	messages, marked := 0, 0
	for _, entry := range entries {
		switch entry.Kind {
		case "message":
			m := entry.Message
			if marked > 0 || entry.Warning != "" || m == nil || len(entry.Targets) == 0 || seen[m.ID] || messages >= r.Limit || !validCheckMessage(m) || entry.Target != m.Target {
				return core.CheckResult{}, ErrReadUnavailable
			}
			seen[m.ID] = true
			order = append(order, m)
			messages++
			for _, i := range entry.Targets {
				if i < 0 || i >= len(pages) || last[i] == m.ID {
					return core.CheckResult{}, ErrReadUnavailable
				}
				last[i] = m.ID
				routed[i] = append(routed[i], m.ID)
				switch {
				case i == inbox:
					if p.inboxMessage(m) {
						pages[i].Messages = append(pages[i].Messages, m)
					}
				case thread(i):
					root := r.Threads[i-threads].ID
					if m.ID != root && m.ThreadID != root {
						return core.CheckResult{}, ErrReadUnavailable
					}
					if !protocol.IsChannel(m.Target) || !p.scope.readable(m.Target) {
						withheld[i] = true
					}
					pages[i].Messages = append(pages[i].Messages, m)
				default:
					if m.Target != pages[i].Room {
						return core.CheckResult{}, ErrReadUnavailable
					}
					pages[i].Messages = append(pages[i].Messages, m)
				}
			}
		case "page":
			if marked >= len(pages) {
				return core.CheckResult{}, ErrReadUnavailable
			}
			page := &pages[marked]
			if entry.Warning != "" {
				// AIRC warns only of a followed thread it no longer retains.
				if !thread(marked) || entry.Target != page.Room || last[marked] != "" {
					return core.CheckResult{}, auth.ErrForbidden
				}
				page.Status = "expired"
				marked++
				continue
			}
			// A retained page cut short before its first message keeps
			// AIRC's "oldest" cursor; its next check starts there again.
			if entry.Cursor == "*" && after[marked] == "retained" && last[marked] == "" && entry.Status == "more" {
				entry.Cursor = ""
			}
			if entry.Target != page.Room || entry.Message != nil || entry.Status != "ok" && entry.Status != "more" || entry.Cursor != "" && !validHistoryID(entry.Cursor) {
				return core.CheckResult{}, ErrReadUnavailable
			}
			// A page cut short stops after its last message, or before
			// its first when AIRC returned none of them.
			if last[marked] != "" && entry.Status == "more" && entry.Cursor != last[marked] {
				return core.CheckResult{}, ErrReadUnavailable
			}
			page.Cursor, page.Status, page.Gap = entry.Cursor, entry.Status, entry.Gap
			if withheld[marked] {
				page.Messages, page.Cursor, page.Status, page.Gap = []*irc.MessageMetadata{}, "", "withheld", false
			}
			marked++
		default:
			return core.CheckResult{}, ErrReadUnavailable
		}
	}
	if marked != len(pages) {
		return core.CheckResult{}, ErrReadUnavailable
	}
	result := func() core.CheckResult {
		result := core.CheckResult{Rooms: pages[:threads]}
		if len(r.Threads) > 0 {
			result.Threads = pages[threads : threads+len(r.Threads)]
		}
		if inbox >= 0 {
			result.Inbox = &pages[inbox]
		}
		return result
	}
	data, err := json.Marshal(result())
	if err != nil {
		return core.CheckResult{}, ErrReadUnavailable
	}
	if len(data) > core.MaxCheckBytes {
		if !fitCheck(pages, order, routed, after, len(data)) {
			return core.CheckResult{}, store.ErrCapacity
		}
		if data, err = json.Marshal(result()); err != nil || len(data) > core.MaxCheckBytes {
			return core.CheckResult{}, store.ErrCapacity
		}
	}
	return result(), nil
}

// fitCheck cuts a check result of size bytes to MaxCheckBytes by leaving
// out its newest messages, so a backlog of large messages reads as pages
// with more to come rather than a refusal the same positions would meet
// again. Each page keeps a prefix of its messages, as messages are oldest
// first; one cut short has status "more" and its cursor at the last message
// AIRC routed to it that it keeps, or its starting position when it keeps
// none. It reports false when even the oldest message alone does not fit.
func fitCheck(pages []core.CheckPage, order []*irc.MessageMetadata, routed [][]string, after []string, size int) bool {
	cost := func(m *irc.MessageMetadata) int {
		data, _ := json.Marshal(m)
		return len(data) + 1
	}
	// A margin for the statuses and cursors the cut changes.
	const margin = 4 << 10
	shown := map[string]int{} // pages each message appears on
	for _, page := range pages {
		for _, m := range page.Messages {
			shown[m.ID]++
		}
	}
	keep := len(order)
	for keep > 1 && size > core.MaxCheckBytes-margin {
		keep--
		size -= shown[order[keep].ID] * cost(order[keep])
	}
	if size > core.MaxCheckBytes-margin {
		return false
	}
	kept := make(map[string]bool, keep)
	for _, m := range order[:keep] {
		kept[m.ID] = true
	}
	for i := range pages {
		page := &pages[i]
		cut, cursor := false, ""
		for _, id := range routed[i] {
			if !kept[id] {
				cut = true
				break
			}
			cursor = id
		}
		if !cut || page.Status == "withheld" || page.Status == "expired" {
			continue
		}
		page.Messages = slices.DeleteFunc(page.Messages, func(m *irc.MessageMetadata) bool { return !kept[m.ID] })
		if cursor == "" && validHistoryID(after[i]) {
			cursor = after[i]
		}
		page.Status, page.Cursor = "more", cursor
	}
	return true
}

func validCheckMessage(m *irc.MessageMetadata) bool {
	if !validHistoryID(m.ID) || !utf8.ValidString(m.Message) || strings.ContainsRune(m.Message, 0) || len(m.Message) > 4096 || m.Timestamp.IsZero() {
		return false
	}
	from := core.Target{Kind: "direct", Name: m.From}
	if from.Normalize() != nil {
		return false
	}
	return (m.ReplyTo == "" || validHistoryID(m.ReplyTo)) && (m.ThreadID == "" || validHistoryID(m.ThreadID))
}

// emptyCheck reports a snapshot with no news on any page. A withheld or
// expired thread stays so on every check, so it does not end a wait; the
// answer still reports it.
func emptyCheck(result core.CheckResult) bool {
	pages := append(slices.Clip(result.Rooms), result.Threads...)
	if result.Inbox != nil {
		pages = append(slices.Clip(pages), *result.Inbox)
	}
	for _, page := range pages {
		if len(page.Messages) > 0 || page.Status == "more" || page.Gap {
			return false
		}
	}
	return true
}
