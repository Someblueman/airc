package protocol

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// A mention is "@nick" anywhere in a message, or "nick:" at the start of a line,
// which is how agents already address one another. Nicknames here are letters,
// digits, underscore and hyphen so that punctuation around a tag ("(@anvil)",
// "@anvil,") never becomes part of the name.
//
// In regular-expression terms a tag is (?:^|[^A-Za-z0-9_@])@([A-Za-z_][A-Za-z0-9_-]{0,29})
// and an address is ^([A-Za-z_][A-Za-z0-9_-]{0,29}):(?:\s|$); the scanners
// below find exactly those matches without linking a regexp engine.

func nameStart(c byte) bool { return c == '_' || c|0x20 >= 'a' && c|0x20 <= 'z' }
func nameByte(c byte) bool  { return nameStart(c) || c == '-' || c >= '0' && c <= '9' }

// mentionName returns the name at the start of s, at most 30 bytes.
func mentionName(s string) string {
	if s == "" || !nameStart(s[0]) {
		return ""
	}
	n := 1
	for n < len(s) && n < 30 && nameByte(s[n]) {
		n++
	}
	return s[:n]
}

// lineTags returns each tag in a line, as the tag pattern's successive
// non-overlapping matches would: the character before "@", when there is
// one, belongs to the match, so it cannot also end the previous one.
func lineTags(line string, add func(string)) {
	next := 0 // where the next match may begin
	for at := strings.IndexByte(line, '@'); at >= 0; {
		start := at
		if at > 0 {
			_, width := utf8.DecodeLastRuneInString(line[:at])
			start = at - width
			if c := line[start]; width == 1 && (nameByte(c) && c != '-' || c == '@') {
				start = -1
			}
		}
		if start >= next {
			if name := mentionName(line[at+1:]); name != "" {
				add(name)
				next = at + 1 + len(name)
			}
		}
		following := strings.IndexByte(line[at+1:], '@')
		if following < 0 {
			return
		}
		at += 1 + following
	}
}

// lineAddress returns the name a line starts with, followed by ":" and then
// whitespace or the end of the line, or "".
func lineAddress(line string) string {
	name := mentionName(line)
	if name == "" || len(name) == len(line) || line[len(name)] != ':' {
		return ""
	}
	if rest := line[len(name)+1:]; rest != "" && !strings.ContainsRune("\t\n\f\r ", rune(rest[0])) {
		return ""
	}
	return name
}

// Mentions returns the lower-cased nicknames a message tags or addresses, in order
// of first appearance, without duplicates. A name that merely looks like a tag
// ("user@example.com") is not a mention.
func Mentions(body string) []string {
	if !strings.ContainsAny(body, "@:") {
		return nil // both forms need one of these; skip the scans for plain text
	}
	var found []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimRight(name, "-"))
		if name != "" && !seen[name] {
			seen[name] = true
			found = append(found, name)
		}
	}
	var fence byte
	length := 0
	for line := range strings.SplitSeq(body, "\n") {
		mark, count, suffix := CodeFence(line)
		if fence == 0 && count >= 3 {
			fence, length = mark, count
			continue
		}
		if fence != 0 {
			if mark == fence && count >= length && strings.TrimSpace(suffix) == "" {
				fence = 0
			}
			continue
		}
		lineTags(line, add)
		if name := lineAddress(line); name != "" {
			add(name)
		}
	}
	return found
}

// MentionsNick reports whether body tags or addresses nick (case-insensitive).
func MentionsNick(body, nick string) bool {
	nick = strings.ToLower(nick)
	return slices.Contains(Mentions(body), nick)
}

// CodeFence recognizes Markdown backtick/tilde fences, with up to three leading
// spaces. The suffix is a language label on an opening fence, empty on a close.
func CodeFence(line string) (mark byte, count int, suffix string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" || trimmed[0] != '`' && trimmed[0] != '~' {
		return 0, 0, ""
	}
	mark = trimmed[0]
	for count < len(trimmed) && trimmed[count] == mark {
		count++
	}
	return mark, count, trimmed[count:]
}
