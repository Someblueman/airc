package main

import (
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// bodyLines keeps code literal: Markdown, mentions and word wrapping must not
// alter its operators, spacing or indentation. Tabs have already been expanded
// for terminal display; the message itself retains its original whitespace.
func (r *renderer) bodyLines(body string, width int) []string {
	var out []string
	var fence byte
	length := 0
	for line := range strings.SplitSeq(body, "\n") {
		mark, count, suffix := protocol.CodeFence(line)
		if fence == 0 && count >= 3 {
			fence, length = mark, count
			for _, part := range wrapCode(line, width) {
				out = append(out, r.dim(part))
			}
			continue
		}
		if fence != 0 {
			if mark == fence && count >= length && strings.TrimSpace(suffix) == "" {
				fence = 0
				for _, part := range wrapCode(line, width) {
					out = append(out, r.dim(part))
				}
				continue
			}
			for _, part := range wrapCode(line, width) {
				out = append(out, r.fg(221, part))
			}
			continue
		}
		if r.color {
			line = markup(line)
		}
		lines := wrapText(line, width)
		if r.color {
			if len(out) == 0 {
				lines[0] = r.styleAddressee(lines[0])
			}
			lines = r.paint(lines)
		}
		out = append(out, lines...)
	}
	return out
}
