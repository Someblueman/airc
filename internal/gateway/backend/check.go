package backend

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
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
// The inbox page is AIRC's "@nick" selector as the author: direct messages
// AIRC recorded as received by the author's account, and mentions in rooms
// for which mentionable reports true (the rooms its listener lets it read).
func (b *Backend) Check(ctx context.Context, grant *store.ReadGrant, mentionable func(room string) bool) (core.CheckResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.CheckResult{}, err
	}
	r := a.Request
	if r.Operation != core.Check || len(r.Rooms) == 0 && r.Inbox == nil || r.Inbox != nil && !a.Author.Registered {
		return core.CheckResult{}, store.ErrInvalid
	}
	var result core.CheckResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		if client.Features()["CHECK"] != "1" {
			return ErrReadUnsupported
		}
		p := checkPlan{request: r, mentionable: mentionable}
		if r.Inbox != nil {
			// The inbox is proven against the account this session logged
			// in as, never the nickname alone.
			p.nick, p.account = client.Nick(), client.AccountID()
			if !strings.EqualFold(p.nick, a.Author.Nick) || !validHistoryID(p.account) {
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
		woke, err = p.await(overall, client, wait)
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

// checkPlan is one check's native selectors: its rooms, then the author's
// inbox ("@nick") when asked for, with the account that proves a direct
// message was the author's.
type checkPlan struct {
	request       core.Request
	nick, account string
	mentionable   func(room string) bool
}

func (p checkPlan) selectors() []string {
	names := make([]string, 0, len(p.request.Rooms)+1)
	for _, room := range p.request.Rooms {
		names = append(names, room.Name)
	}
	if p.request.Inbox != nil {
		names = append(names, "@"+p.nick)
	}
	return names
}

// wakes reports a live message for the check: in one of its rooms or, when
// it reads the inbox, any message the session observes, as AIRC delivers
// only the observed rooms and the author's inbox.
func (p checkPlan) wakes(event irc.Event) bool {
	m, ok := event.(*irc.MessageEvent)
	if !ok || m.Type != "message" {
		return false
	}
	if p.request.Inbox != nil {
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
		return p.mentionable != nil && p.mentionable(m.Target)
	}
	return strings.EqualFold(m.Target, p.nick) && m.RecipientAccountID == p.account
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

// snapshot runs one CHECK for every selector and validates the pages.
func (p checkPlan) snapshot(ctx context.Context, client *irc.Client, other func(irc.Event) error) (core.CheckResult, error) {
	r := p.request
	request := irc.CheckRequest{IncludeOwn: true, MaxMessages: r.Limit}
	after := make([]string, 0, len(r.Rooms)+1)
	for _, room := range r.Rooms {
		after = append(after, room.After)
	}
	if r.Inbox != nil {
		after = append(after, r.Inbox.After)
	}
	for i, selector := range p.selectors() {
		target := irc.CheckTarget{Target: selector, Limit: r.Limit}
		switch after[i] {
		case "recent":
		case "retained":
			target.After = "*"
		default:
			target.After = after[i]
		}
		request.Targets = append(request.Targets, target)
	}
	entries, err := boundedCheck(ctx, client, request, r.Limit+len(request.Targets), other)
	if err != nil {
		return core.CheckResult{}, err
	}
	if ctx.Err() != nil {
		return core.CheckResult{}, ErrReadUnavailable
	}
	return p.pages(entries, after)
}

// pages turns CHECK's entries (every message once, oldest first, naming the
// selectors whose pages hold it, then one page marker per selector in
// request order) into a page per room and the inbox page. The inbox keeps
// only the author's own direct messages.
func (p checkPlan) pages(entries []irc.CheckEntry, after []string) (core.CheckResult, error) {
	r := p.request
	selectors := p.selectors()
	pages := make([]core.CheckPage, len(selectors))
	last := make([]string, len(selectors)) // each page's last message, kept or not
	for i, selector := range selectors {
		pages[i] = core.CheckPage{Room: selector, Messages: []*irc.MessageMetadata{}}
	}
	inbox := -1
	if r.Inbox != nil {
		inbox = len(selectors) - 1
	}
	seen := map[string]bool{}
	messages, marked := 0, 0
	for _, entry := range entries {
		if entry.Warning != "" {
			return core.CheckResult{}, auth.ErrForbidden
		}
		switch entry.Kind {
		case "message":
			m := entry.Message
			if marked > 0 || m == nil || len(entry.Targets) == 0 || seen[m.ID] || messages >= r.Limit || !validCheckMessage(m) || entry.Target != m.Target {
				return core.CheckResult{}, ErrReadUnavailable
			}
			seen[m.ID] = true
			messages++
			for _, i := range entry.Targets {
				if i < 0 || i >= len(pages) || last[i] == m.ID {
					return core.CheckResult{}, ErrReadUnavailable
				}
				last[i] = m.ID
				if i == inbox {
					if p.inboxMessage(m) {
						pages[i].Messages = append(pages[i].Messages, m)
					}
					continue
				}
				if m.Target != pages[i].Room {
					return core.CheckResult{}, ErrReadUnavailable
				}
				pages[i].Messages = append(pages[i].Messages, m)
			}
		case "page":
			if marked >= len(pages) {
				return core.CheckResult{}, ErrReadUnavailable
			}
			page := &pages[marked]
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
			marked++
		default:
			return core.CheckResult{}, ErrReadUnavailable
		}
	}
	if marked != len(pages) {
		return core.CheckResult{}, ErrReadUnavailable
	}
	result := core.CheckResult{Rooms: pages}
	if inbox >= 0 {
		result.Rooms, result.Inbox = pages[:inbox], &pages[inbox]
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > core.MaxCheckBytes {
		return core.CheckResult{}, store.ErrCapacity
	}
	return result, nil
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

// emptyCheck reports a snapshot with no news on any page.
func emptyCheck(result core.CheckResult) bool {
	pages := result.Rooms
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
