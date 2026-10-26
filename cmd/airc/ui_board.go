package main

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// The /board view lays a room's handoffs out by state, from the room's
// handoffs listing: a query view like /search, redrawn when a live handoff
// step lands in the room, closed with /close.

// boardGroups are the board's sections in order. A declined or withdrawn
// handoff is over and has no place on it.
var boardGroups = []struct{ title, state string }{
	{"Open", "open"},
	{"In progress", "accepted"},
	{"Blocked", "blocked"},
	{"Done (last week)", "done"},
}

// boardIDWidth is how much of a handoff's ID a board line shows.
const boardIDWidth = 8

func boardName(room string) string { return "board:" + room }

// showBoard opens or redraws the board of v.room. A refresh redraws an open
// board without switching to it, and is dropped once the board is closed.
func (m *uiModel) showBoard(v boardIn) {
	name := boardName(v.room)
	b := m.find(name)
	if b == nil {
		if v.refresh {
			return
		}
		if m.queryViews() >= 16 {
			m.setStatus("Close a query view before opening another (limit 16)", true)
			return
		}
		b = newBuffer(name, bufQuery)
		b.returnTo = m.current
		m.buffers = append(m.buffers, b)
	}
	b.query = &uiQuery{Kind: "board", Target: v.room}
	b.help = boardLines(v.entries, m.now())
	b.topic = boardHeader(v.entries)
	b.version++
	if !v.refresh {
		m.switchTo(name)
	}
}

// queryViews counts the open query views, which are bounded.
func (m *uiModel) queryViews() int {
	count := 0
	for _, b := range m.buffers {
		if b.kind == bufQuery {
			count++
		}
	}
	return count
}

// isBoard reports a board view.
func isBoard(b *uiBuffer) bool {
	return b != nil && b.kind == bufQuery && b.query != nil && b.query.Kind == "board"
}

// boardHeader counts the room's handoffs by state for the view's header.
func boardHeader(entries []irc.ChatEntry) string {
	counts := map[string]int{}
	for _, e := range entries {
		if e.Action == "handoff" {
			counts[e.State]++
		}
	}
	return fmt.Sprintf("%d open · %d in progress · %d blocked · %d done this week · /next refreshes · /close returns · /handoffs for full IDs",
		counts["open"], counts["accepted"], counts["blocked"], counts["done"])
}

// boardLines lays the handoffs out by state, each line the start of its ID,
// who holds it (anyone, while a room handoff is open to all), the offer's
// age and its first line, then why it is blocked or where its result is.
func boardLines(entries []irc.ChatEntry, now time.Time) []helpLine {
	var lines []helpLine
	for _, group := range boardGroups {
		var rows []helpLine
		for _, e := range entries {
			if e.Action != "handoff" || e.State != group.state {
				continue
			}
			holder := cmp.Or(e.Holder, "anyone")
			var offered time.Time
			offer := ""
			if e.Message != nil {
				offered, offer = e.Message.Timestamp, firstLine(e.Message.Message)
				if e.Holder == "" && e.Message.For != "" {
					holder = "for " + e.Message.For
				}
			}
			body := holder + "  " + age(offered, now) + "  " + offer
			switch {
			case e.State == "blocked":
				body += "  blocked " + age(e.BlockedAt, now) + ": " + firstLine(e.BlockedReason)
			case e.Link != "":
				body += "  " + e.Link
			}
			rows = append(rows, helpLine{head: e.ID[:min(boardIDWidth, len(e.ID))], body: body})
		}
		lines = append(lines, helpLine{head: fmt.Sprintf("%s (%d)", group.title, len(rows))})
		lines = append(lines, rows...)
	}
	return lines
}

// firstLine is the first line of text, cleaned and cut for a board line.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return ellipsize(strings.TrimSpace(cleanText(line)), 120)
}
