package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// A message that shares a paste is drawn collapsed, as its caption and one
// line naming the paste; /paste ID opens the paste in a read-only query view
// that shows its lines a page at a time, like /thread, and /close returns.

// pastePageLines is how many lines of a paste a page shows.
const pastePageLines = 200

type (
	pasteIn struct { // a paste read for /paste ID
		entry   irc.ChatEntry
		content []byte
	}
	pastePageIn struct { // /next or /first on a paste view
		name  string
		first bool
	}
)

func pasteViewName(id string) string { return "paste:" + id }

// isPasteView reports a /paste view.
func isPasteView(b *uiBuffer) bool {
	return b != nil && b.kind == bufQuery && b.query != nil && b.query.Kind == "paste"
}

// collapsePaste draws a message that shares a paste as its caption and one
// line naming the paste, with how to open it, in place of the reference
// line the body carries for clients that know no pastes.
func (r *renderer) collapsePaste(m *irc.MessageEvent, body string) string {
	if m.Paste == nil {
		return body
	}
	hint := r.pasteHint
	if hint == "" {
		hint = "airc paste get "
	}
	line := fmt.Sprintf("[paste %s, %s, %d lines] %s%s", cleanText(m.Paste.Name), protocol.FormatPasteSize(m.Paste.Size), m.Paste.Lines, hint, m.Paste.ID)
	if caption := protocol.PasteCaption(body, *m.Paste); caption != "" {
		return caption + "\n" + line
	}
	return line
}

// showPaste opens, or redraws, the view of a paste that was read.
func (m *uiModel) showPaste(v pasteIn) {
	if v.entry.Paste == nil {
		return
	}
	name := pasteViewName(v.entry.Paste.ID)
	b := m.find(name)
	if b == nil {
		if m.queryViews() >= 16 {
			m.setStatus("Close a query view before opening another (limit 16)", true)
			return
		}
		b = newBuffer(name, bufQuery)
		b.returnTo = m.current
		m.buffers = append(m.buffers, b)
	}
	b.query = &uiQuery{Kind: "paste", Target: v.entry.Paste.ID}
	b.pasteLines = strings.Split(strings.TrimSuffix(string(v.content), "\n"), "\n")
	b.pasteEntry = v.entry
	b.pasteShown = min(pastePageLines, len(b.pasteLines))
	b.topic = pasteHeader(b)
	b.version++
	m.switchTo(name)
}

// pagePaste shows the next page of a paste view, or starts it over.
func (m *uiModel) pagePaste(v pastePageIn) {
	b := m.find(v.name)
	if !isPasteView(b) {
		return
	}
	if v.first {
		b.pasteShown = min(pastePageLines, len(b.pasteLines))
	} else {
		b.pasteShown = min(b.pasteShown+pastePageLines, len(b.pasteLines))
	}
	b.topic = pasteHeader(b)
	b.version++
}

// pasteHeader is a paste view's header: what the paste is, who shared it
// and which of its lines are shown.
func pasteHeader(b *uiBuffer) string {
	p := b.pasteEntry.Paste
	paging := "/first restarts"
	if b.pasteShown < len(b.pasteLines) {
		paging = "/next continues"
	}
	return fmt.Sprintf("%s · %s · %s · by %s · lines 1-%d of %d · %s · /close returns",
		cleanText(p.Name), cleanText(p.Language), protocol.FormatPasteSize(p.Size), cleanText(b.pasteEntry.From), b.pasteShown, len(b.pasteLines), paging)
}

// renderPasteLines draws the shown lines of a paste view, numbered, long
// lines wrapped under their number.
func renderPasteLines(b *uiBuffer, width int) []string {
	digits := len(strconv.Itoa(max(len(b.pasteLines), 1)))
	body := max(width-digits-2, 10)
	var out []string
	for i, line := range b.pasteLines[:b.pasteShown] {
		number := sty("2", fmt.Sprintf("%*d", digits, i+1))
		wrapped := wrapCode(cleanText(line), body)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for j, part := range wrapped {
			if j == 0 {
				out = append(out, number+"  "+part)
			} else {
				out = append(out, strings.Repeat(" ", digits+2)+part)
			}
		}
	}
	if b.pasteShown < len(b.pasteLines) {
		out = append(out, sty("2", fmt.Sprintf(" %d more lines · /next", len(b.pasteLines)-b.pasteShown)))
	}
	return out
}
