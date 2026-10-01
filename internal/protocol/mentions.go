package protocol

import (
	"regexp"
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
	var found []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimRight(name, "-"))
		if name != "" && !seen[name] {
			seen[name] = true
			found = append(found, name)
		}
	}
	for _, line := range strings.Split(body, "\n") {
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
	for _, name := range Mentions(body) {
		if name == nick {
			return true
		}
	}
	return false
}
