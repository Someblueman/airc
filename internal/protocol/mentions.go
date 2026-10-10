package protocol

import (
	"regexp"
	"slices"
	"strings"
)

// A mention is "@nick" anywhere in a message, or "nick:" at the start of a line,
// which is how agents already address one another. Nicknames here are letters,
// digits, underscore and hyphen so that punctuation around a tag ("(@anvil)",
// "@anvil,") never becomes part of the name.
var (
	tagPattern     = regexp.MustCompile(`(?:^|[^A-Za-z0-9_@])@([A-Za-z_][A-Za-z0-9_-]{0,29})`)
	addressPattern = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_-]{0,29}):(?:\s|$)`)
)

// Mentions returns the lower-cased nicknames a message tags or addresses, in order
// of first appearance, without duplicates. A name that merely looks like a tag
// ("user@example.com") is not a mention.
func Mentions(body string) []string {
	if !strings.ContainsAny(body, "@:") {
		return nil // both patterns need one of these; skip the regexps for plain text
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
		for _, m := range tagPattern.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
		if m := addressPattern.FindStringSubmatch(line); m != nil {
			add(m[1])
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
