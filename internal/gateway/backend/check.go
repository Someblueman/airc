package backend

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
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
func (b *Backend) Check(ctx context.Context, grant *store.ReadGrant) (core.CheckResult, error) {
	a, err := grant.Consume(time.Now())
	if err != nil {
		return core.CheckResult{}, err
	}
	if a.Request.Operation != core.Check || len(a.Request.Rooms) == 0 {
		return core.CheckResult{}, store.ErrInvalid
	}
	var result core.CheckResult
	err = b.withRead(ctx, a, false, func(overall context.Context, client *irc.Client) error {
		if client.Features()["CHECK"] != "1" {
			return ErrReadUnsupported
		}
		r := a.Request
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
				if unobserveRooms(client, r.Rooms) != nil {
					_ = client.Close()
				}
			}()
			all, err := observeRooms(overall, client, r.Rooms)
			if err != nil {
				return err
			}
			// AIRC skips a room it would not show this author; the snapshot
			// then refuses it.
			waiting = all
		}
		var woke bool
		news := func(event irc.Event) error {
			if m, ok := event.(*irc.MessageEvent); ok && m.Type == "message" && checkRoom(r.Rooms, m.Target) {
				woke = true
			}
			return nil
		}
		result, err = checkSnapshot(overall, client, r, news)
		if err != nil || !waiting || woke || !emptyCheck(result) {
			return err
		}
		// Nothing new: wait without holding a session slot.
		b.release(lane, store.LookupAttempt)
		woke, err = awaitRoomMessage(overall, client, r.Rooms, wait)
		if !b.reacquire(lane) || err != nil || !woke {
			return err
		}
		result, err = checkSnapshot(overall, client, r, nil)
		return err
	})
	if err != nil {
		return core.CheckResult{}, err
	}
	return result, nil
}

// observeRooms observes rooms and reports whether AIRC confirmed every one.
// AIRC skips a room the session may not read without replying, so a ping
// marks the end of the confirmations.
func observeRooms(ctx context.Context, client *irc.Client, rooms []core.CheckRoom) (bool, error) {
	names := roomNames(rooms)
	if err := client.Observe(names...); err != nil {
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
				return len(confirmed) == len(names), nil
			case rejectedRead(raw):
				return false, ErrReadUnavailable
			}
		case <-ctx.Done():
			return false, ErrReadUnavailable
		}
	}
}

func unobserveRooms(client *irc.Client, rooms []core.CheckRoom) error {
	return client.Raw("UNOBSERVE " + strings.Join(roomNames(rooms), ","))
}

// awaitRoomMessage reports whether a live message arrived in one of rooms
// before wait ended. It pings every checkKeepAlive, as a quiet wait may
// outlast the session's read timeout; the pongs are discarded here, or by
// the session's next reuse.
func awaitRoomMessage(ctx context.Context, client *irc.Client, rooms []core.CheckRoom, wait time.Duration) (bool, error) {
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
			if m, ok := event.(*irc.MessageEvent); ok && m.Type == "message" && checkRoom(rooms, m.Target) {
				return true, nil
			}
		case <-timer.C:
			return false, nil
		case <-ctx.Done():
			return false, ErrReadUnavailable
		}
	}
}

// checkSnapshot runs one CHECK for every room of r and validates the pages.
func checkSnapshot(ctx context.Context, client *irc.Client, r core.Request, other func(irc.Event) error) (core.CheckResult, error) {
	request := irc.CheckRequest{IncludeOwn: true, MaxMessages: r.Limit}
	for _, room := range r.Rooms {
		target := irc.CheckTarget{Target: room.Name, Limit: r.Limit}
		switch room.After {
		case "recent":
		case "retained":
			target.After = "*"
		default:
			target.After = room.After
		}
		request.Targets = append(request.Targets, target)
	}
	entries, err := boundedCheck(ctx, client, request, r.Limit+len(r.Rooms), other)
	if err != nil {
		return core.CheckResult{}, err
	}
	if ctx.Err() != nil {
		return core.CheckResult{}, ErrReadUnavailable
	}
	return checkPages(entries, r)
}

// checkPages turns CHECK's entries (every message once, oldest first, then
// one page marker per room in request order) into a page per room.
func checkPages(entries []irc.CheckEntry, r core.Request) (core.CheckResult, error) {
	result := core.CheckResult{Rooms: make([]core.CheckPage, len(r.Rooms))}
	for i, room := range r.Rooms {
		result.Rooms[i] = core.CheckPage{Room: room.Name, Messages: []*irc.MessageMetadata{}}
	}
	seen := map[string]bool{}
	messages, pages := 0, 0
	for _, entry := range entries {
		if entry.Warning != "" {
			return core.CheckResult{}, auth.ErrForbidden
		}
		switch entry.Kind {
		case "message":
			m := entry.Message
			if pages > 0 || m == nil || len(entry.Targets) != 1 || entry.Targets[0] < 0 || entry.Targets[0] >= len(r.Rooms) || seen[m.ID] || messages >= r.Limit || !validCheckMessage(m) {
				return core.CheckResult{}, ErrReadUnavailable
			}
			page := &result.Rooms[entry.Targets[0]]
			if entry.Target != page.Room || m.Target != page.Room {
				return core.CheckResult{}, ErrReadUnavailable
			}
			seen[m.ID] = true
			messages++
			page.Messages = append(page.Messages, m)
		case "page":
			if pages >= len(r.Rooms) {
				return core.CheckResult{}, ErrReadUnavailable
			}
			page := &result.Rooms[pages]
			// A retained room cut short before its first message keeps
			// AIRC's "oldest" cursor; its next check starts there again.
			if entry.Cursor == "*" && r.Rooms[pages].After == "retained" && len(page.Messages) == 0 && entry.Status == "more" {
				entry.Cursor = ""
			}
			if entry.Message != nil || entry.Status != "ok" && entry.Status != "more" || entry.Cursor != "" && !validHistoryID(entry.Cursor) {
				return core.CheckResult{}, ErrReadUnavailable
			}
			if entry.Target != page.Room {
				return core.CheckResult{}, ErrReadUnavailable
			}
			// A page cut short stops after its last message, or before
			// its first when AIRC returned none of them.
			if n := len(page.Messages); n > 0 && entry.Status == "more" && entry.Cursor != page.Messages[n-1].ID {
				return core.CheckResult{}, ErrReadUnavailable
			}
			page.Cursor, page.Status, page.Gap = entry.Cursor, entry.Status, entry.Gap
			pages++
		default:
			return core.CheckResult{}, ErrReadUnavailable
		}
	}
	if pages != len(r.Rooms) {
		return core.CheckResult{}, ErrReadUnavailable
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

// emptyCheck reports a snapshot with no news in any room.
func emptyCheck(result core.CheckResult) bool {
	for _, page := range result.Rooms {
		if len(page.Messages) > 0 || page.Status == "more" || page.Gap {
			return false
		}
	}
	return true
}

func checkRoom(rooms []core.CheckRoom, target string) bool {
	for _, room := range rooms {
		if room.Name == target {
			return true
		}
	}
	return false
}

func roomNames(rooms []core.CheckRoom) []string {
	names := make([]string, len(rooms))
	for i, room := range rooms {
		names[i] = room.Name
	}
	return names
}
