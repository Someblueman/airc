package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var listItemPattern = regexp.MustCompile(`^([-*•]|\d+[.)]) `)

// ellipsize shortens s to at most n runes, marking the cut with "…".
func ellipsize(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:max(n-1, 0)]) + "…"
}

// cleanText removes control characters and bidirectional overrides so a
// message cannot rewrite the viewer's terminal or visually spoof another line.
func cleanText(text string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || isMark(c) || (c >= 0x202A && c <= 0x202E) || (c >= 0x2066 && c <= 0x2069) {
			return -1
		}
		return c
	}, text)
}

// cleanBody is cleanText for multi-line text: line breaks stay, tabs become spaces.
func cleanBody(text string) string {
	text = strings.ReplaceAll(text, "\t", "    ")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = cleanText(line)
	}
	return strings.Join(lines, "\n")
}

// wrapText wraps each line of text to width runes. Leading indentation is kept,
// and wrapped lines of a bullet or numbered item hang under its text.
func wrapText(text string, width int) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		hang := indent
		if m := listItemPattern.FindString(trimmed); m != "" {
			hang += utf8.RuneCountInString(m)
		}
		out = append(out, wrapWords(strings.Fields(trimmed), width, indent, hang)...)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func wrapWords(words []string, width, indent, hang int) []string {
	if indent >= width-8 {
		indent, hang = 0, 0
	}
	hang = min(hang, width-8)
	var lines []string
	current := strings.Repeat(" ", indent)
	used := indent
	fresh := true
	flush := func() {
		lines = append(lines, current)
		current, used, fresh = strings.Repeat(" ", hang), hang, true
	}
	for _, word := range words {
		runes := []rune(word)
		if !fresh && used+1+visibleLen(runes) > width {
			flush()
		}
		for fresh && used+visibleLen(runes) > width {
			head, tail := cutVisible(runes, width-used)
			current += string(head)
			runes = tail
			flush()
		}
		if !fresh {
			current += " "
			used++
		}
		current += string(runes)
		used += visibleLen(runes)
		fresh = false
	}
	return append(lines, current)
}

// visibleLen counts the runes that occupy a terminal cell; style marks do not.
func visibleLen(runes []rune) int {
	n := 0
	for _, c := range runes {
		if !isMark(c) {
			n++
		}
	}
	return n
}

// cutVisible splits runes after n visible runes, keeping any marks that follow
// the cut with the tail so style state stays in order.
func cutVisible(runes []rune, n int) (head, tail []rune) {
	seen := 0
	for i, c := range runes {
		if isMark(c) {
			continue
		}
		if seen == n {
			return runes[:i], runes[i:]
		}
		seen++
	}
	return runes, nil
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

func visibleWidth(s string) int { return utf8.RuneCountInString(ansiPattern.ReplaceAllString(s, "")) }

// padVisible pads s with spaces to w visible columns, clipping if it is wider.
func padVisible(s string, w int) string {
	width := visibleWidth(s)
	if width > w {
		return clipVisible(s, w)
	}
	return s + strings.Repeat(" ", w-width)
}

// clipVisible cuts s after w visible columns, keeping escape sequences intact and
// closing any style left open.
func clipVisible(s string, w int) string {
	var b strings.Builder
	shown := 0
	for i := 0; i < len(s); {
		if loc := ansiPattern.FindStringIndex(s[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(s[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if shown >= w {
			break
		}
		b.WriteRune(r)
		shown++
		i += size
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// fitPlain truncates plain text to w columns with an ellipsis, then pads it.
func fitPlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ellipsize(s, w)
	return s + strings.Repeat(" ", w-utf8.RuneCountInString(s))
}

// Wrap by characters rather than words, preserving every display character.
func wrapCode(line string, width int) []string {
	runes := []rune(line)
	width = max(width, 1)
	var out []string
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	return append(out, string(runes))
}
