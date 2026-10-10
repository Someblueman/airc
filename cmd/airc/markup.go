package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Inline styles are marked with zero-width private-use runes while text is
// wrapped, then turned into ANSI sequences afterwards so a span that wraps onto
// several lines stays styled on each of them. cleanText removes these runes from
// incoming text, so a message cannot produce them itself.
const (
	markCodeOn  = '\ue000'
	markCodeOff = '\ue001'
	markBoldOn  = '\ue002'
	markBoldOff = '\ue003'
	markLinkOn  = '\ue004'
	markLinkOff = '\ue005'

	markMentionOn  = '\ue006' // followed by one rune from markColorBase naming the nick's palette entry
	markMentionOff = '\ue007'
	markColorBase  = '\ue100'
)

func isMark(c rune) bool { return c >= '\ue000' && c <= '\ue1ff' }

var (
	inlinePattern    = regexp.MustCompile("(`[^`\n]+`)|(\\*\\*[^*\n]+\\*\\*)|(https?://[^\\s)>\\]]+)|((?:^|[^A-Za-z0-9_@])@[A-Za-z_][A-Za-z0-9_-]*)")
	bulletPattern    = regexp.MustCompile(`^(\s*)[-*] `)
	headingPattern   = regexp.MustCompile(`^(\s*)#{1,6} +(.*)$`)
	addresseePattern = regexp.MustCompile(`^([A-Za-z_\[\]\\^{|}][A-Za-z0-9_\[\]\\^{|}-]{0,29}):( |$)`)
)

// markup replaces light markdown (headings, bullets, **bold**, `code`, links)
// with zero-width style marks, one source line at a time. Spans never cross a
// line break in the source, so an unmatched marker is left as literal text.
func markup(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + string(markBoldOn) + m[2] + string(markBoldOff)
			continue
		}
		line = bulletPattern.ReplaceAllString(line, "$1• ")
		lines[i] = inlinePattern.ReplaceAllStringFunc(line, func(span string) string {
			switch {
			case strings.HasPrefix(span, "`"):
				return string(markCodeOn) + span[1:len(span)-1] + string(markCodeOff)
			case strings.HasPrefix(span, "**"):
				return string(markBoldOn) + span[2:len(span)-2] + string(markBoldOff)
			case strings.HasPrefix(span, "http"):
				return string(markLinkOn) + span + string(markLinkOff)
			default:
				// An @mention, possibly with the character before it.
				at := strings.IndexByte(span, '@')
				return span[:at] + string(markMentionOn) + string(markColorBase+rune(nickIndex(span[at+1:]))) + span[at:] + string(markMentionOff)
			}
		})
	}
	return strings.Join(lines, "\n")
}

func (r *renderer) markStyle(mark, arg rune) string {
	switch mark {
	case markCodeOn:
		return "38;5;221"
	case markBoldOn:
		return "1"
	case markMentionOn:
		return fmt.Sprintf("1;38;5;%d", nickPalette[int(arg-markColorBase)%len(nickPalette)])
	default:
		return "4;38;5;75"
	}
}

type openStyle struct{ mark, arg rune }

// paint turns style marks into ANSI sequences. A style still open at the end of
// a line is closed there and reopened on the next, so no line leaves the
// terminal in a styled state.
func (r *renderer) paint(lines []string) []string {
	var open []openStyle
	out := make([]string, len(lines))
	for i, line := range lines {
		var b strings.Builder
		for _, style := range open {
			b.WriteString("\x1b[" + r.markStyle(style.mark, style.arg) + "m")
		}
		runes := []rune(line)
		for j := 0; j < len(runes); j++ {
			c := runes[j]
			switch c {
			case markCodeOn, markBoldOn, markLinkOn, markMentionOn:
				style := openStyle{mark: c}
				if c == markMentionOn && j+1 < len(runes) {
					j++
					style.arg = runes[j]
				}
				open = append(open, style)
				b.WriteString("\x1b[" + r.markStyle(style.mark, style.arg) + "m")
			case markCodeOff, markBoldOff, markLinkOff, markMentionOff:
				for k, o := range slices.Backward(open) {
					if o.mark == c-1 {
						open = append(open[:k], open[k+1:]...)
						break
					}
				}
				b.WriteString("\x1b[0m")
				for _, style := range open {
					b.WriteString("\x1b[" + r.markStyle(style.mark, style.arg) + "m")
				}
			default:
				if !isMark(c) {
					b.WriteRune(c)
				}
			}
		}
		if len(open) > 0 {
			b.WriteString("\x1b[0m")
		}
		out[i] = b.String()
	}
	return out
}

// styleAddressee colors a leading "name:" in the named agent's color, so
// "builder: please ..." shows who is being asked.
func (r *renderer) styleAddressee(line string) string {
	m := addresseePattern.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	name := m[1]
	return r.bold(r.fg(nickColor(name), name+":")) + line[len(name)+1:]
}
